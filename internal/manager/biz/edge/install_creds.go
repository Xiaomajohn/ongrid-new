package edge

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// InstallEdgeIssuer is the concrete EdgeIssuer used by the installjob
// worker. It wraps Usecase.Create — which already mints an access_key
// + secret_key, argon2id-hashes the secret, and seeds default plugin
// configs — and exposes only the (access, secret) pair the worker
// pipes into install.sh.
//
// The install path uses an auto-generated name so the operator can
// tell which edge rows came from "click install" vs. "manually create
// edge in the UI":
//
//	auto-install-<deviceID>-<unix>
//
// Operators can rename later via the SPA's Edges page.
type InstallEdgeIssuer struct {
	uc  *Usecase
	log *slog.Logger
}

// NewInstallEdgeIssuer wires the concrete. uc must be non-nil; the
// install worker calls CreateEdgeForDevice before installer.Install so
// a nil usecase is a wiring bug, not a soft-failure. log may be nil.
func NewInstallEdgeIssuer(uc *Usecase, log *slog.Logger) *InstallEdgeIssuer {
	if log == nil {
		log = slog.Default()
	}
	return &InstallEdgeIssuer{
		uc:  uc,
		log: log.With(slog.String("comp", "edge-install-issuer")),
	}
}

// CreateEdgeForDevice mints a fresh edge row keyed against deviceID
// (via the auto-generated name) and returns the plaintext access_key
// + secret_key. The secret_key is only ever returned here — the rest
// of the codebase only stores its argon2id hash. The worker MUST pipe
// both keys into install.sh BEFORE ClearCredentialSnap wipes the SSH
// password/key from the job row, otherwise the agent will start up
// with the prior (now rotated) secret and the install won't complete.
//
// createdBy is nil: install jobs are not attributable to a specific
// operator (the HTTP handler that enqueued the install doesn't always
// carry a user id through to this layer, and the audit trail in
// install_job_events already records which job triggered the create).
func (i *InstallEdgeIssuer) CreateEdgeForDevice(ctx context.Context, deviceID uint64) (string, string, error) {
	if i == nil || i.uc == nil {
		return "", "", fmt.Errorf("edge: InstallEdgeIssuer not wired (nil uc)")
	}
	if deviceID == 0 {
		return "", "", fmt.Errorf("edge: CreateEdgeForDevice: deviceID is 0")
	}

	name := fmt.Sprintf("auto-install-%d-%d", deviceID, time.Now().Unix())
	res, err := i.uc.Create(ctx, name, nil)
	if err != nil {
		return "", "", fmt.Errorf("edge: InstallEdgeIssuer.Create: %w", err)
	}
	if res == nil {
		// Defensive — Usecase.Create should never return (nil, nil),
		// but if it does we don't want to panic the worker.
		return "", "", fmt.Errorf("edge: InstallEdgeIssuer.Create: nil result")
	}
	i.log.Info("install-edge creds minted",
		slog.Uint64("device_id", deviceID),
		slog.Uint64("edge_id", res.Edge.ID),
		slog.String("access_key", res.AccessKey),
	)
	return res.AccessKey, res.SecretKey, nil
}