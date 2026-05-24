//go:build darwin

// S7 runtime-share probe (macos-port-execution.md §S7). This is the white-box half of the
// S7 smoke test, and now a REGRESSION GUARD for the shipped base/<tag> virtio-fs shape: the
// single host virtio-fs device is mounted once at a base, and each session is a named subdir
// <base>/<tag> that appears/disappears live as the host swaps the device's share (SetShare).
//
// Why a driver-level test and not vmctl: it drives the driver's AttachWorkspace/DetachWorkspace
// (pure host-side SetShare on the live device) and guestd's mount RPC directly, so it can put
// the device through the single→multi→single transitions and probe the guest at each step
// without the broker's mounts map. The two invariants it pins:
//   - the lone "workspace" tag is a SingleDirectoryShare → files at the device root (S6); and
//   - any session set is a MultipleDirectoryShare → files at <base>/<tag>, stable for ANY count,
//     so adding/removing one session never moves another's path (the S7 verdict).
//
// This boots a real VM, so it is gated behind ATELIER_VZ_SMOKE and only runs on Apple
// Silicon. The test binary instantiates Virtualization.framework in-process, so it must be
// codesigned with com.apple.security.virtualization before it runs — use
// scripts/s7-smoke-darwin.sh, which compiles, signs, and runs it for you.
package vmm_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlagedo/atelier/services/internal/rpc"
	"github.com/jlagedo/atelier/services/internal/vmm"
	"github.com/jlagedo/atelier/services/internal/vsock"
)

const probeVMID = "s7probe"

// dummyMountPort is a non-zero placeholder: guestd's mount RPC rejects port 0, but the
// virtio-fs path ignores the port entirely (it is tag-addressed). See guestd mountShare.
const dummyMountPort uint32 = 564

func TestS7RuntimeShareProbe(t *testing.T) {
	if os.Getenv("ATELIER_VZ_SMOKE") == "" {
		t.Skip("S7 probe boots a real VM; set ATELIER_VZ_SMOKE=1 and run the signed binary via scripts/s7-smoke-darwin.sh")
	}
	bundle := os.Getenv("ATELIER_BUNDLE_DIR")
	if bundle == "" {
		t.Fatal("ATELIER_BUNDLE_DIR must point at the darwin-arm64-vz bundle dir")
	}
	cfg := vmm.VMConfig{
		ID:         probeVMID,
		KernelPath: filepath.Join(bundle, "vmlinuz"),
		InitrdPath: filepath.Join(bundle, "initrd"),
		RootFSPath: filepath.Join(bundle, "rootfs.raw"),
	}

	// Three host dirs, each with a unique sentinel file so we can tell which share
	// surfaced where in the guest.
	base := t.TempDir()
	dirA := sentinelDir(t, base, "A", "a.txt", "alpha")
	dirB := sentinelDir(t, base, "B", "b.txt", "bravo")
	dirC := sentinelDir(t, base, "C", "c.txt", "charlie")

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	d := vmm.NewDriver(log)
	ctx := context.Background()

	mustOK(t, d.Create(ctx, cfg), "create")
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = d.Stop(stopCtx, probeVMID)
	})
	mustOK(t, d.Start(ctx, probeVMID), "start")

	// DialGuest already retries ~10s for guestd to bind its vsock listener after boot.
	conn, err := d.DialGuest(ctx, probeVMID, vsock.GuestRPCPort)
	if err != nil {
		t.Fatalf("DialGuest: %v", err)
	}
	gc := rpc.NewClient(conn)
	defer gc.Close()

	if out, code := guestSh(t, gc, "uname -m; id -u"); code != 0 {
		t.Fatalf("guest sanity exec failed (code=%d): %q", code, out)
	} else {
		t.Logf("guest up: %s", strings.ReplaceAll(strings.TrimSpace(out), "\n", " / "))
	}

	// ---- Phase 1 — legacy single workspace (S6 regression guard) -----------------
	// A lone "workspace" tag is a SingleDirectoryShare: the directory lands at the device
	// root, so mounting the device at /workspace shows the files directly.
	mustOK(t, d.AttachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{HostPath: dirA, Tag: vsock.WorkspaceShareTag}), "attach A as workspace")
	guestMount(t, gc, vsock.WorkspaceShareTag, "/workspace")
	if out, _ := guestSh(t, gc, "ls -A /workspace"); !strings.Contains(out, "a.txt") {
		t.Errorf("FAIL S6: /workspace should contain a.txt, got %q", oneline(out))
	} else {
		t.Logf("[P1 legacy single] /workspace = %q", oneline(out))
	}
	// Free the device for the per-session phase: unmount the legacy view, drop the share.
	guestUnmount(t, gc, "/workspace")
	mustOK(t, d.DetachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{Tag: vsock.WorkspaceShareTag}), "detach workspace")

	// ---- Phase 2 — per-session base mount + the live-add spike --------------------
	// The shipped shape: the single device is mounted ONCE at the base (/sessions), and each
	// session is a named subdir <base>/<tag> (MultipleDirectoryShare). A single per-session
	// share is pinned to Multiple too, so it lands at /sessions/<tag>, never the device root —
	// that stability keeps a later session from moving an earlier one's path.
	mustOK(t, d.AttachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{HostPath: dirB, Tag: "s1"}), "attach B as s1")
	guestMount(t, gc, vsock.WorkspaceShareTag, "/sessions/s1") // mounts the device once at /sessions
	if out, _ := guestSh(t, gc, "cat /sessions/s1/b.txt 2>&1"); !strings.Contains(out, "bravo") {
		t.Errorf("FAIL: a lone per-session share should land at /sessions/s1 (not the root), got %q", oneline(out))
	} else {
		t.Logf("[P2 single session] /sessions/s1/b.txt = %q", oneline(out))
	}

	// The S7 spike: add a second session while the base is already mounted and DO NOT remount.
	// It must appear on its own as /sessions/s2.
	mustOK(t, d.AttachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{HostPath: dirC, Tag: "s2"}), "attach C as s2 (live)")
	if out, _ := guestSh(t, gc, "cat /sessions/s2/c.txt 2>&1"); !strings.Contains(out, "charlie") {
		t.Errorf("FAIL S7 live-add: /sessions/s2 should appear with NO remount, got %q", oneline(out))
	} else {
		t.Logf("[P2 live add, NO remount] /sessions/s2/c.txt = %q", oneline(out))
	}

	// ---- Phase 3 — sibling-safe detach + idempotency -----------------------------
	// Detach the first session; its subdir must vanish while the sibling stays — again with
	// no guest remount (the host SetShare rebuild is the only action).
	mustOK(t, d.DetachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{Tag: "s1"}), "detach s1")
	gone, _ := guestSh(t, gc, "ls -A /sessions/s1 2>&1")
	if strings.Contains(gone, "b.txt") {
		t.Errorf("FAIL: /sessions/s1 should be gone after detach, got %q", oneline(gone))
	}
	if out, _ := guestSh(t, gc, "cat /sessions/s2/c.txt 2>&1"); !strings.Contains(out, "charlie") {
		t.Errorf("FAIL: sibling /sessions/s2 should survive s1 detach, got %q", oneline(out))
	} else {
		t.Logf("[P3 sibling-safe] s1 gone (%q), s2 intact", oneline(gone))
	}

	// Idempotent re-detach of an absent tag is a no-op at the driver.
	mustOK(t, d.DetachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{Tag: "s1"}), "re-detach s1 (idempotent)")

	t.Logf("==================== S7 SHIPPED-SHAPE VERIFIED ====================")
	t.Logf("legacy single workspace at /workspace (S6) ... OK")
	t.Logf("per-session single at /sessions/<tag> ........ OK (stable, no root collapse)")
	t.Logf("live add of a 2nd session, NO remount ........ OK")
	t.Logf("sibling-safe detach + idempotent re-detach ... OK")
	t.Logf("==================================================================")
}

// guestSh runs a /bin/sh -c script in the guest as root (Privileged bypasses the
// bubblewrap sandbox so we can read host-owned virtio-fs files and mount points), and
// returns the combined stdout+stderr plus the exit code.
func guestSh(t *testing.T, c *rpc.Client, script string) (string, int) {
	t.Helper()
	var res struct {
		ExitCode int `json:"exitCode"`
	}
	var buf bytes.Buffer
	onNotify := func(method string, params json.RawMessage) {
		if method != "exec/output" {
			return
		}
		var o struct {
			Stream string `json:"stream"`
			Data   string `json:"data"`
		}
		if json.Unmarshal(params, &o) != nil {
			return
		}
		if b, err := base64.StdEncoding.DecodeString(o.Data); err == nil {
			buf.Write(b)
		}
	}
	params := map[string]any{
		"cmd":        "/bin/sh",
		"args":       []string{"-c", script},
		"privileged": true,
	}
	if err := c.CallStream(context.Background(), "exec", params, &res, onNotify); err != nil {
		t.Fatalf("guest exec %q: %v", script, err)
	}
	return buf.String(), res.ExitCode
}

// guestMount asks guestd to mount the host virtio-fs device for the given target. For a
// root-level target (/workspace) guestd mounts the device there directly; for a per-session
// target (/sessions/<tag>) it mounts the single device once at the base (/sessions) and the
// subdir appears on its own. On virtio-fs the mount source is the device's fixed tag, so we
// pass vsock.WorkspaceShareTag (the tag arg is validated but not used as the source).
func guestMount(t *testing.T, c *rpc.Client, tag, target string) {
	t.Helper()
	params := map[string]any{"port": dummyMountPort, "tag": tag, "target": target}
	if err := c.Call(context.Background(), "mount", params, nil); err != nil {
		t.Fatalf("guest mount %s at %s: %v", tag, target, err)
	}
}

// guestUnmount asks guestd to unmount the share at target (a real unmount for a legacy
// /workspace mount; a no-op for a per-session subdir under the shared base).
func guestUnmount(t *testing.T, c *rpc.Client, target string) {
	t.Helper()
	if err := c.Call(context.Background(), "unmount", map[string]any{"target": target}, nil); err != nil {
		t.Fatalf("guest unmount %s: %v", target, err)
	}
}

// sentinelDir makes <base>/<name> containing one file with known contents.
func sentinelDir(t *testing.T, base, name, file, contents string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustOK(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func oneline(s string) string { return strings.Join(strings.Fields(s), " ") }
