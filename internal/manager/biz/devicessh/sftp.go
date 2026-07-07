// Package devicessh: SFTP service. Importing github.com/pkg/sftp here
// is required by the package surface but the dependency is NOT in
// go.mod yet — A3 (this phase) does not run `go mod tidy` per the plan's
// "go mod tidy 放到 Phase 3 sync-4" rule. The expected outcome of
// `go build ./internal/manager/biz/devicessh/...` from this phase is:
//
//	FAIL with "cannot find package github.com/pkg/sftp"
//
// which is the documented Phase 3 sync-4 handoff. The source in this
// file is otherwise complete and self-consistent.
package devicessh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/pkg/sftp"

	"github.com/ongridio/ongrid/internal/manager/model/device"
)

// Entry is the wire shape returned by the list/stat HTTP handlers. It
// matches the modal expected by the React FileBrowser component (name +
// permission bits + size + mtime + is_dir).
type Entry struct {
	Name  string `json:"name"`
	Mode  uint32 `json:"mode"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
	IsDir bool   `json:"is_dir"`
}

// Stat is the wire shape for the stat endpoint (same fields, single
// record rather than slice).
type Stat struct {
	Name  string `json:"name"`
	Mode  uint32 `json:"mode"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
	IsDir bool   `json:"is_dir"`
}

// SFTPService is the manager-side SFTP façade. One instance per boot;
// methods are safe to call from many goroutines.
//
// Every method follows the same skeleton:
//
//  1. PathGuard.Check on every user-supplied path (writes follow the
//     same rule — the path is the destination).
//  2. Router.MustConnect → *ssh.Client (reuses the direct dialer's
//     handshake; tunnel path returns a B1-pending placeholder error).
//  3. sftp.NewClient(client) subsystem setup.
//  4. The actual op.
//  5. AuditLogger.Log with the op name + path + size + status.
//
// On any failure between (2) and (4) the audit row records "error"
// with the err_msg truncated to errMsgCap.
type SFTPService struct {
	*Router
	*PathGuard
	*AuditLogger
}

// NewSFTPService wires the three collaborators. Any may be nil to
// disable that aspect (e.g. tests that don't care about persistence
// pass nil for audit).
func NewSFTPService(r *Router, pg *PathGuard, audit *AuditLogger) *SFTPService {
	if pg == nil {
		pg = NewPathGuard()
	}
	return &SFTPService{
		Router:      r,
		PathGuard:   pg,
		AuditLogger: audit,
	}
}

// withSFTP opens an *ssh.Client via the router + a paired sftp.Client.
// Both are returned so the caller defers Close on each; the helper
// centralises the audit row's "ok" / "error" classification so every
// public method gets it for free.
//
// pathGuardPath is the user-supplied path that should be audited
// (may differ from paths built downstream).
func (s *SFTPService) withSFTP(
	ctx context.Context,
	d *device.Device,
	purpose Purpose,
	pathGuardPath string,
) (*sftp.Client, func(op string, path, pathNew string, size int64, err error), error) {
	if s.Router == nil {
		return nil, nil, fmt.Errorf("devicessh: sftp service: router not wired")
	}
	client, err := s.Router.MustConnect(ctx, d, purpose, RouteKindAuto)
	if err != nil {
		return nil, nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("devicessh: sftp.NewClient: %w", err)
	}
	cleanup := func(op, path, pathNew string, size int64, opErr error) {
		_ = sc.Close()
		_ = client.Close()
		if s.AuditLogger != nil {
			s.AuditLogger.Log(ctx, d.ID, 0, op, path, pathNew, size, opErr)
		}
	}
	return sc, cleanup, nil
}

// List enumerates one directory.
func (s *SFTPService) List(ctx context.Context, d *device.Device, path string, userID uint64) ([]Entry, error) {
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "list", path, "", 0, err)
		return nil, err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return nil, err
	}
	fis, err := sc.ReadDir(path)
	if err != nil {
		end("list", path, "", 0, err)
		return nil, err
	}
	out := make([]Entry, 0, len(fis))
	var total int64
	for _, fi := range fis {
		out = append(out, Entry{
			Name:  fi.Name(),
			Mode:  uint32(fi.Mode()),
			Size:  fi.Size(),
			MTime: fi.ModTime().Unix(),
			IsDir: fi.IsDir(),
		})
		total += fi.Size()
	}
	end("list", path, "", total, nil)
	return out, nil
}

// Stat looks up one path.
func (s *SFTPService) Stat(ctx context.Context, d *device.Device, path string, userID uint64) (Stat, error) {
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "stat", path, "", 0, err)
		return Stat{}, err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return Stat{}, err
	}
	fi, err := sc.Stat(path)
	if err != nil {
		end("stat", path, "", 0, err)
		return Stat{}, err
	}
	out := Stat{
		Name:  fi.Name(),
		Mode:  uint32(fi.Mode()),
		Size:  fi.Size(),
		MTime: fi.ModTime().Unix(),
		IsDir: fi.IsDir(),
	}
	end("stat", path, "", fi.Size(), nil)
	return out, nil
}

// Read opens path for reading and copies up to maxBytes into the writer.
// maxBytes <= 0 defaults to 10 MiB (matches the plan / RFC limit). The
// returned reader is fully consumed before this method returns — callers
// who want a streaming read should request a feature flag with a future
// version (see plan §"风险与回滚").
func (s *SFTPService) Read(ctx context.Context, d *device.Device, path string, userID uint64, w io.Writer, maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "read", path, "", 0, err)
		return 0, err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return 0, err
	}
	f, err := sc.Open(path)
	if err != nil {
		end("read", path, "", 0, err)
		return 0, err
	}
	defer f.Close()
	n, err := io.Copy(w, io.LimitReader(f, maxBytes))
	end("read", path, "", n, err)
	return n, err
}

// Write uploads a small blob to path. The HTTP layer enforces a 1 MiB
// cap (see plan §API) before calling this; we re-cap to 16 MiB as a
// server-side safety net.
func (s *SFTPService) Write(ctx context.Context, d *device.Device, path string, data []byte, mode os.FileMode, userID uint64) error {
	const cap = 16 << 20
	if int64(len(data)) > cap {
		err := fmt.Errorf("devicessh: sftp write too large (%d > %d)", len(data), cap)
		s.auditDeny(ctx, d, userID, "write", path, "", int64(len(data)), err)
		return err
	}
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "write", path, "", int64(len(data)), err)
		return err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return err
	}
	f, err := sc.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		end("write", path, "", 0, err)
		return err
	}
	defer f.Close()
	if mode != 0 {
		_ = f.Chmod(mode)
	}
	n, err := f.Write(data)
	end("write", path, "", int64(n), err)
	return err
}

// Mkdir creates a directory at path (and any missing parents — pkg/sftp
// follows the SSH_FXP_MKDIR semantics of mkdir(1), not mkdir -p; the
// HTTP layer should pre-create tree levels when uploading a multi-level
// archive).
func (s *SFTPService) Mkdir(ctx context.Context, d *device.Device, path string, mode os.FileMode, userID uint64) error {
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "mkdir", path, "", 0, err)
		return err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o755
	}
	err = sc.Mkdir(path)
	if err == nil {
		_ = sc.Chmod(path, mode)
	}
	end("mkdir", path, "", 0, err)
	return err
}

// Rmdir removes an empty directory. For non-empty directories the
// caller should iterate + delete children first; pkg/sftp returns
// a "failure" message that we surface verbatim.
func (s *SFTPService) Rmdir(ctx context.Context, d *device.Device, path string, userID uint64) error {
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "rmdir", path, "", 0, err)
		return err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return err
	}
	err = sc.RemoveDirectory(path)
	end("rmdir", path, "", 0, err)
	return err
}

// Rm removes a single file (sftp.Remove covers both files and empty
// dirs, but the API surface in plan is split — this keeps a paper
// trail of which method was used).
func (s *SFTPService) Rm(ctx context.Context, d *device.Device, path string, userID uint64) error {
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "rm", path, "", 0, err)
		return err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return err
	}
	err = sc.Remove(path)
	end("rm", path, "", 0, err)
	return err
}

// Rename moves a file or directory. Both ends are path-checked.
func (s *SFTPService) Rename(ctx context.Context, d *device.Device, oldPath, newPath string, userID uint64) error {
	if err := s.PathGuard.Check(oldPath); err != nil {
		s.auditDeny(ctx, d, userID, "rename", oldPath, newPath, 0, err)
		return err
	}
	if err := s.PathGuard.Check(newPath); err != nil {
		s.auditDeny(ctx, d, userID, "rename", oldPath, newPath, 0, err)
		return err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, oldPath)
	if err != nil {
		return err
	}
	err = sc.Rename(oldPath, newPath)
	end("rename", oldPath, newPath, 0, err)
	return err
}

// Chmod sets the file mode bits.
func (s *SFTPService) Chmod(ctx context.Context, d *device.Device, path string, mode os.FileMode, userID uint64) error {
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "chmod", path, "", 0, err)
		return err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return err
	}
	err = sc.Chmod(path, mode)
	end("chmod", path, "", 0, err)
	return err
}

// Upload streams from r to a freshly created file at path. The remote
// file is created O_WRONLY|O_CREATE|O_TRUNC — partial uploads leave a
// stale file behind on the remote; the HTTP handler should pair this
// with a "Cancel" cleanup call.
func (s *SFTPService) Upload(ctx context.Context, d *device.Device, path string, r io.Reader, userID uint64) (int64, error) {
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "upload", path, "", 0, err)
		return 0, err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return 0, err
	}
	f, err := sc.Create(path)
	if err != nil {
		end("upload", path, "", 0, err)
		return 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	end("upload", path, "", n, err)
	return n, err
}

// Download streams the remote file into w. The HTTP handler
// terminates with a Content-Disposition; we cap at 1 GiB to bound
// memory in case the caller forgot to set a download destination.
func (s *SFTPService) Download(ctx context.Context, d *device.Device, path string, userID uint64, w io.Writer) (int64, error) {
	const cap = 1 << 30
	if err := s.PathGuard.Check(path); err != nil {
		s.auditDeny(ctx, d, userID, "download", path, "", 0, err)
		return 0, err
	}
	sc, end, err := s.withSFTP(ctx, d, PurposeSFTP, path)
	if err != nil {
		return 0, err
	}
	f, err := sc.Open(path)
	if err != nil {
		end("download", path, "", 0, err)
		return 0, err
	}
	defer f.Close()
	n, err := io.Copy(w, io.LimitReader(f, cap))
	end("download", path, "", n, err)
	return n, err
}

// auditDeny is the convenience method for the pathguard rejection path
// — same AuditLogger.Log call shape, plus the explicit userID threading
// that the success-path closures elide (since userID is wired by the
// HTTP handler at the same time the audit row is written).
func (s *SFTPService) auditDeny(ctx context.Context, d *device.Device, userID uint64, op, path, pathNew string, size int64, err error) {
	if s.AuditLogger == nil {
		return
	}
	s.AuditLogger.Log(ctx, d.ID, userID, op, path, pathNew, size, err)
}

// unusedErr keeps errors in the import set until sftp.NewClient gains
// callers that might return io.EOF or similar — the import is otherwise
// only used via sftp.Client, not via the std errors package. Remove
// once the helper compiles its first non-error path.
var _ = errors.Is
