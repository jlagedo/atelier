package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jlagedo/atelier/services/internal/rpc"
	"github.com/jlagedo/atelier/services/internal/vsock"
)

// workspaceGuestPath is where the workspace 9p share is mounted inside the guest.
const workspaceGuestPath = "/workspace"

// The Files door (design.md §10): read/list auto-allowed in the workspace;
// writes gated + audited; every path canonicalized against the workspace root,
// rejecting ".." and symlinks that escape. The broker mediates the I/O itself
// (host-side), so the jail is enforced at the privileged boundary (§8) — the
// same files are surfaced to guest exec via the 9p /workspace share (S3.1).

// ReadFileParams asks to read a workspace-relative path.
type ReadFileParams struct {
	Path string `json:"path"`
}

// WriteFileParams asks to write Content to a workspace-relative path. Content is
// base64 (std encoding) so the wire is binary-safe — Excel/Word/PDF and any
// non-UTF-8 bytes survive a JSON string field intact (same discipline as exec
// output, design.md §8).
type WriteFileParams struct {
	Path    string `json:"path"`
	Content string `json:"content"` // base64-encoded
}

// FileContent is the readFile result. Content is base64-encoded; Size is the
// decoded byte length.
type FileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"` // base64-encoded
	Size    int    `json:"size"`
}

func (b *Broker) readFile(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "readFile", "files"); err != nil {
		return nil, err
	}
	var p ReadFileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()}
	}
	abs, err := b.jail(p.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	return FileContent{
		Path:    p.Path,
		Content: base64.StdEncoding.EncodeToString(data),
		Size:    len(data),
	}, nil
}

func (b *Broker) writeFile(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "writeFile", "files"); err != nil {
		return nil, err
	}
	var p WriteFileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()}
	}
	data, err := base64.StdEncoding.DecodeString(p.Content)
	if err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "content must be base64: " + err.Error()}
	}
	abs, err := b.jail(p.Path)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	return nil, nil
}

// jail resolves a request path against the currently-attached workspace root,
// returning a wire error if no workspace is attached or the path escapes it.
func (b *Broker) jail(rel string) (string, error) {
	abs, err := jailPath(b.currentWorkspace(), rel)
	if err != nil {
		return "", &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()}
	}
	return abs, nil
}

// AttachWorkspaceParams shares host folder Path into VM ID. Without Tag it is the
// legacy single share at /workspace (swap-replaced; the Files-door jail root).
// With Tag (+ Target) it is one of several concurrent per-session shares (S6.1):
// mounted at Target, added alongside others. Port 0 lets the broker allocate.
type AttachWorkspaceParams struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly,omitempty"`
	Target   string `json:"target,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Port     int    `json:"port,omitempty"`
}

// DetachWorkspaceParams removes a share from VM ID. Empty Tag removes the legacy
// /workspace share.
type DetachWorkspaceParams struct {
	ID  string `json:"id"`
	Tag string `json:"tag,omitempty"`
}

// attachWorkspace shares a host folder into the running VM and tells guestd to
// mount it (design.md §10 — S3.1; concurrent per-session shares — S6.1). Host
// side: grant + add the Plan9 share via ModifyComputeSystem. Guest side: guestd
// mounts it. Legacy mode (no Tag) mounts at /workspace, swap-replacing any prior
// default share and setting the Files-door jail root. Multi mode (Tag set) mounts
// at Target and is added alongside other shares — the basis for many sessions in
// one VM.
func (b *Broker) attachWorkspace(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "attachWorkspace", "files"); err != nil {
		return nil, err
	}
	var p AttachWorkspaceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()}
	}
	if p.ID == "" || p.Path == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "id and path are required"}
	}
	root, err := canonicalRoot(p.Path)
	if err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "workspace path: " + err.Error()}
	}

	tag := strings.TrimSpace(p.Tag)
	target := strings.TrimSpace(p.Target)
	legacy := tag == ""
	port := uint32(p.Port)
	if legacy {
		tag = vsock.WorkspaceShareTag
		target = workspaceGuestPath
		if port == 0 {
			port = vsock.WorkspacePlan9Port
		}
	}
	if target == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "target is required when tag is set"}
	}

	// Re-attach/swap: if this tag is already mounted, drop it first (best-effort).
	if old, ok := b.removeMount(tag); ok {
		_ = b.guestUnmount(ctx, p.ID, old.guestPath)
		_ = b.vms.DetachWorkspace(ctx, p.ID, old.tag, old.port)
		if legacy {
			b.setWorkspace("")
		}
	}

	m := b.addMount(mountInfo{hostPath: root, guestPath: target, tag: tag, port: port, readOnly: p.ReadOnly})

	if err := b.vms.AttachWorkspace(ctx, p.ID, root, p.ReadOnly, m.tag, m.port); err != nil {
		b.removeMount(tag)
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	if err := b.guestMount(ctx, p.ID, m); err != nil {
		_ = b.vms.DetachWorkspace(ctx, p.ID, m.tag, m.port) // roll back the host share
		b.removeMount(tag)
		return nil, err
	}
	if legacy {
		b.setWorkspace(root)
	}
	return nil, nil
}

// detachWorkspace unmounts a share in the guest and removes the host share,
// closing that Files door. Empty Tag detaches the legacy /workspace share.
func (b *Broker) detachWorkspace(ctx context.Context, params json.RawMessage) (any, error) {
	if err := b.authorize(ctx, "detachWorkspace", "files"); err != nil {
		return nil, err
	}
	var p DetachWorkspaceParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()}
	}
	if p.ID == "" {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "id is required"}
	}
	tag := strings.TrimSpace(p.Tag)
	if tag == "" {
		tag = vsock.WorkspaceShareTag
	}
	m, ok := b.removeMount(tag)
	if !ok {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "no such workspace share: " + tag}
	}
	if err := b.guestUnmount(ctx, p.ID, m.guestPath); err != nil {
		return nil, err
	}
	if err := b.vms.DetachWorkspace(ctx, p.ID, m.tag, m.port); err != nil {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	if tag == vsock.WorkspaceShareTag {
		b.setWorkspace("")
	}
	return nil, nil
}

// guestMount tells guestd to mount share m (the guest half of attach).
func (b *Broker) guestMount(ctx context.Context, id string, m mountInfo) error {
	conn, err := b.vms.DialGuest(ctx, id)
	if err != nil {
		return &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	defer conn.Close()
	return rpc.NewClient(conn).Call(ctx, "mount", map[string]any{
		"port":   m.port,
		"tag":    m.tag,
		"target": m.guestPath,
	}, nil)
}

// guestUnmount tells guestd to unmount the share at target (guest half of detach).
func (b *Broker) guestUnmount(ctx context.Context, id, target string) error {
	conn, err := b.vms.DialGuest(ctx, id)
	if err != nil {
		return &rpc.Error{Code: rpc.CodeInternal, Message: err.Error()}
	}
	defer conn.Close()
	return rpc.NewClient(conn).Call(ctx, "unmount", map[string]any{
		"target": target,
	}, nil)
}

// canonicalRoot resolves the workspace root to an absolute, symlink-free path so
// the jail can compare against a stable prefix. Called once at New.
func canonicalRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

// jailPath canonicalizes rel against root and confirms it stays inside (the
// Files-door jail, design.md §10). It rejects: a closed door (empty root), an
// empty/absolute rel, ".." escapes, and symlinks that resolve outside root.
// root must already be canonical (see canonicalRoot). The returned path is the
// cleaned absolute path to operate on (it may not exist yet, e.g. a new file).
func jailPath(root, rel string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("files door not configured (no workspace)")
	}
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	// Relative-only contract. filepath.IsAbs is OS-specific (on Windows it needs a
	// drive), so also reject a leading separator and a volume name explicitly so
	// "/etc/passwd" and "C:\x" are rejected on every platform.
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" || rel[0] == '/' || rel[0] == '\\' {
		return "", fmt.Errorf("path must be relative to the workspace: %q", rel)
	}

	clean := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if !withinRoot(root, clean) {
		return "", fmt.Errorf("path escapes the workspace: %q", rel)
	}

	// Symlink check: resolve the deepest existing ancestor (the target itself may
	// not exist yet for a write) and confirm the real path is still inside root —
	// catches a symlink under the workspace that points outside it.
	resolved, err := resolveExisting(clean)
	if err != nil {
		return "", err
	}
	if !withinRoot(root, resolved) {
		return "", fmt.Errorf("path escapes the workspace via symlink: %q", rel)
	}
	return clean, nil
}

// withinRoot reports whether p is root itself or lies beneath it.
func withinRoot(root, p string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(os.PathSeparator))
}

// resolveExisting EvalSymlinks the longest existing prefix of p, then re-appends
// the non-existent tail. This lets the jail vet not-yet-created targets (writes)
// while still resolving any symlinks along the existing portion of the path.
func resolveExisting(p string) (string, error) {
	tail := ""
	cur := p
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(resolved, tail), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the volume/filesystem root without finding an existing dir.
			return p, nil
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}
