package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	devicebiz "github.com/ongridio/ongrid/internal/manager/biz/device"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// EdgeLookup is the narrow surface getSSHInfo needs beyond the device
// Usecase: list edges for a device, then read their online flag. The
// biz/edge junction repo satisfies this; main.go passes a thin adapter
// (a func or a struct) when constructing the Handler. May be nil —
// when nil, the SSH info endpoint reports edge_online=false and the
// SPA falls back to the device row's own Online column.
type EdgeLookup interface {
	ListEdgesForDevice(ctx context.Context, deviceID uint64) ([]EdgeLink, error)
}

// EdgeLink carries only the columns getSSHInfo reads.
type EdgeLink struct {
	ID     uint64
	Status string
}

// sshCredentialsReq is the wire body for PUT. Replaces the whole SSH
// block in one shot; per-field rotation uses DELETE ?kind=.
type sshCredentialsReq struct {
	Kind    string `json:"kind"`           // "password" | "key"
	Value   string `json:"value"`          // secret (password OR private key)
	Host    string `json:"host,omitempty"` // optional fallback host
	Port    int    `json:"port,omitempty"` // 0 → 22
	User    string `json:"user,omitempty"` // SSH username
	HostKey string `json:"host_key,omitempty"`
}

// sshCredentialsResp is the wire shape returned by GET. Includes
// EdgeOnline so the SPA can show "agent online" alongside the
// non-secret summary without a second roundtrip.
type sshCredentialsResp struct {
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port"`
	User        string `json:"user,omitempty"`
	AuthKind    string `json:"auth_kind"`
	HasPassword bool   `json:"has_password"`
	HasKey      bool   `json:"has_key"`
	// Password / Key carry the plaintext secret (internal ops tool, no
	// envelope). Empty string means "this auth kind is not configured".
	// Frontends gate visibility behind HasPassword / HasKey booleans
	// (still useful as a quick "is set?" hint in lists).
	Password   string `json:"password,omitempty"`
	Key        string `json:"key,omitempty"`
	HostKey    string `json:"host_key,omitempty"`
	LastSeenAt any    `json:"last_seen_at,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	EdgeOnline bool   `json:"edge_online"`
}

// putSSHCredentials replaces the SSH block. Admin only. Single-PUT
// semantics keeps the contract small; per-field rotation goes through
// DELETE ?kind=.
func (h *Handler) putSSHCredentials(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body sshCredentialsReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	kind := strings.TrimSpace(body.Kind)
	if kind != "password" && kind != "key" {
		writeErr(w, fmt.Errorf("%w: kind must be password or key", errs.ErrInvalid))
		return
	}
	value := body.Value
	if strings.TrimSpace(value) == "" {
		writeErr(w, fmt.Errorf("%w: value required (password or private key)", errs.ErrInvalid))
		return
	}
	user := strings.TrimSpace(body.User)
	if user == "" {
		writeErr(w, fmt.Errorf("%w: user required", errs.ErrInvalid))
		return
	}
	creds := devicebiz.SSHCredentials{
		Host:     strings.TrimSpace(body.Host),
		Port:     body.Port,
		User:     user,
		AuthKind: kind,
		HostKey:  strings.TrimSpace(body.HostKey),
	}
	switch kind {
	case "password":
		creds.Password = value
	case "key":
		creds.Key = value
	}
	if err := h.uc.SetSSHCredentials(r.Context(), id, creds); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getSSHInfo returns the non-secret half + the owning edge's online
// flag. SPA renders this in the device-detail "远程访问" panel. Auth:
// any authenticated tenant member.
func (h *Handler) getSSHInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	wire, err := h.uc.GetSSHCredentials(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	resp := sshCredentialsResp{
		Host:        wire.Host,
		Port:        wire.Port,
		User:        wire.User,
		AuthKind:    wire.AuthKind,
		HasPassword: wire.HasPassword,
		HasKey:      wire.HasKey,
		Password:    wire.Password,
		Key:         wire.Key,
		HostKey:     wire.HostKey,
		LastSeenAt:  wire.LastSeenAt,
		LastError:   wire.LastError,
		EdgeOnline:  h.lookupOnlineEdge(r.Context(), id),
	}
	writeJSON(w, http.StatusOK, resp)
}

// lookupOnlineEdge reads edge-online reachability via the (optional)
// EdgeLookup adapter main.go wired. Any error path returns false so
// the endpoint never 500s on this side-channel fact — the SPA still
// has the device row's own Online column for the primary indicator.
func (h *Handler) lookupOnlineEdge(ctx context.Context, deviceID uint64) bool {
	if h.edges == nil {
		return false
	}
	rows, err := h.edges.ListEdgesForDevice(ctx, deviceID)
	if err != nil {
		return false
	}
	for _, e := range rows {
		if e.Status == "online" {
			return true
		}
	}
	return false
}

// deleteSSHCredentials clears ONE side of the SSH secret per call.
// ?kind=password wipes SSHPassword, ?kind=key wipes SSHKey. The
// operator UI uses this for "rotate password" without forcing a
// full PUT /ssh-credentials roundtrip.
//
// Admin only; 204 on success, 400 on a bad kind, 404 when the
// device doesn't exist.
func (h *Handler) deleteSSHCredentials(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if err := h.uc.ClearSSHCredentialsField(r.Context(), id, kind); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
