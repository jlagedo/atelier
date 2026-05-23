//go:build darwin

// S7 runtime-share probe (macos-port-execution.md §S7). This is the white-box half
// of the S7 smoke test: it answers the plan's single remaining unverified spike
// (macos-port-plan.md, validation #1) — does the guest see a virtio-fs share that was
// added *after* start(), and with what topology when several are attached at once?
//
// Why a driver-level test and not vmctl: the broker's attachWorkspace does host
// SetShare *and* a guest `mount -t virtiofs <tag>` in one step, and rolls the host
// share back if that mount fails (broker/files.go). Because there is exactly one
// virtio-fs device with one fixed tag ("workspace", driver_darwin.go Create), a
// per-share-tag mount of a second share can't match the device, so the broker path
// can never hold a stable multi-share state — it always rolls back. The *driver's*
// AttachWorkspace, by contrast, is pure host-side SetShare with no guest mount and no
// rollback, so it is the only seam that lets us put the live device into a
// MultipleDirectoryShare and then probe the guest directly.
//
// This boots a real VM, so it is gated behind ATELIER_VZ_SMOKE and only runs on
// Apple Silicon. The test binary instantiates Virtualization.framework in-process, so
// it must be codesigned with com.apple.security.virtualization before it runs — use
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

	// Observations we fold into the verdict block at the end.
	var (
		singleOK      bool // S6 shape: one share visible directly at its mountpoint
		liveAddSeen   bool // a share added after start() became visible WITHOUT a remount
		remountSeen   bool // ...and/or became visible after a fresh guest mount
		subdirTopo    bool // MultipleDirectoryShare exposes entries as named subdirs
		flipBackOK    bool // dropping back to one share returns to the Single (direct) shape
		existingMount string
	)

	// ---- Phase 1 — single share (reproduce S6) -----------------------------------
	mustOK(t, d.AttachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{HostPath: dirA, Tag: "workspace"}), "attach A as workspace")
	guestMount(t, gc, "workspace", "/sessions/p")
	out, _ := guestSh(t, gc, "ls -A /sessions/p")
	t.Logf("[P1 single] /sessions/p = %q", oneline(out))
	singleOK = strings.Contains(out, "a.txt")
	if !singleOK {
		t.Errorf("FAIL: single-share /sessions/p should contain a.txt (S6 regression), got %q", oneline(out))
	}

	// ---- Phase 2 — the live-add question -----------------------------------------
	// Add a second share while the VM runs and /sessions/p is already mounted. This swaps
	// the device's share from Single{workspace} to Multiple{workspace,s1} via the
	// forked vz binding's runtime SetShare.
	mustOK(t, d.AttachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{HostPath: dirB, Tag: "s1"}), "attach B as s1")

	// (a) Existing /sessions/p mount, no remount: did the topology change under our feet?
	existing, _ := guestSh(t, gc, "echo root:; ls -A /sessions/p; echo s1sub:; ls -A /sessions/p/s1 2>&1; echo wssub:; ls -A /sessions/p/workspace 2>&1")
	existingMount = oneline(existing)
	t.Logf("[P2 live-add, NO remount] /sessions/p = %q", existingMount)
	liveAddSeen = strings.Contains(existing, "b.txt")

	// (b) Fresh mount after the swap (the "remount nudge").
	guestMount(t, gc, "workspace", "/sessions/p2")
	fresh, _ := guestSh(t, gc, "echo root:; ls -A /sessions/p2; echo s1sub:; ls -A /sessions/p2/s1 2>&1; echo wssub:; ls -A /sessions/p2/workspace 2>&1")
	t.Logf("[P2 live-add, fresh mount] /sessions/p2 = %q", oneline(fresh))
	remountSeen = strings.Contains(fresh, "b.txt")
	// Topology: in a MultipleDirectoryShare the device root holds one named subdir per
	// entry, so we expect b.txt under /sessions/p2/s1, not at /sessions/p2 directly.
	bUnderS1, _ := guestSh(t, gc, "cat /sessions/p2/s1/b.txt 2>&1")
	subdirTopo = strings.Contains(bUnderS1, "bravo")
	t.Logf("[P2 topology] cat /sessions/p2/s1/b.txt = %q", oneline(bUnderS1))

	// ---- Phase 3 — multi-session churn + idempotency -----------------------------
	mustOK(t, d.AttachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{HostPath: dirC, Tag: "s2"}), "attach C as s2")
	guestMount(t, gc, "workspace", "/sessions/p3")
	three, _ := guestSh(t, gc, "ls -A /sessions/p3; echo '--'; cat /sessions/p3/s1/b.txt 2>&1; cat /sessions/p3/s2/c.txt 2>&1")
	t.Logf("[P3 three shares] /sessions/p3 = %q", oneline(three))

	// Detach the middle one; the other two must stay.
	mustOK(t, d.DetachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{Tag: "s1"}), "detach s1")
	guestMount(t, gc, "workspace", "/sessions/p4")
	afterDetach, _ := guestSh(t, gc, "ls -A /sessions/p4; echo '--'; ls -A /sessions/p4/s1 2>&1; cat /sessions/p4/s2/c.txt 2>&1")
	t.Logf("[P3 after detach s1] /sessions/p4 = %q", oneline(afterDetach))

	// Idempotent re-detach of an absent tag is a no-op at the driver.
	mustOK(t, d.DetachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{Tag: "s1"}), "re-detach s1 (idempotent)")

	// Drop back to a single share: the driver flips Multiple -> Single, so a fresh
	// mount should once again show the directory directly (not under a subdir).
	mustOK(t, d.DetachWorkspace(ctx, probeVMID, vmm.WorkspaceShare{Tag: "s2"}), "detach s2")
	guestMount(t, gc, "workspace", "/sessions/p5")
	flip, _ := guestSh(t, gc, "ls -A /sessions/p5")
	t.Logf("[P3 flip back to single] /sessions/p5 = %q", oneline(flip))
	flipBackOK = strings.Contains(flip, "a.txt") && !strings.Contains(flip, "workspace")

	// ---- Verdict inputs ----------------------------------------------------------
	// These are observations for the macos-port-plan.md §Files Door verdict, not hard
	// pass/fail — the whole point of S7 is to discover the behavior. Only the S6
	// regression check above is allowed to fail the test.
	t.Logf("==================== S7 VERDICT INPUTS ====================")
	t.Logf("single-share direct mount works (S6) ........ %v", singleOK)
	t.Logf("live add visible WITHOUT a remount nudge ..... %v", liveAddSeen)
	t.Logf("live add visible AFTER a fresh guest mount ... %v", remountSeen)
	t.Logf("MultipleDirectoryShare => named subdirs ...... %v", subdirTopo)
	t.Logf("flips back to Single (direct) on 1 share ..... %v", flipBackOK)
	t.Logf("existing-mount view after a live swap: %s", existingMount)
	t.Logf("Interpretation:")
	switch {
	case liveAddSeen:
		t.Logf("  -> host-adds, guest sees it live. The one-VM/many-session model holds")
		t.Logf("     with NO remount; record this as the primary shape.")
	case remountSeen:
		t.Logf("  -> host-adds-then-guest-mounts is the primary shape (a fresh mount is")
		t.Logf("     the nudge). One VM/many sessions still holds; document the remount step.")
	default:
		t.Logf("  -> live add did NOT surface even after a remount. Walk the fallback")
		t.Logf("     ladder in the plan (staging symlinks -> controlled restart ->")
		t.Logf("     one-VM-per-session) and record which one S7 adopts.")
	}
	if subdirTopo {
		t.Logf("  -> multi-session target semantics: mount the single device once at a base")
		t.Logf("     and address each session as <base>/<tag>; per-tag mounts won't work")
		t.Logf("     (this is why the current broker multi path rolls back).")
	}
	t.Logf("==========================================================")
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

// guestMount asks guestd to mount the virtio-fs device tag at target (creating target).
func guestMount(t *testing.T, c *rpc.Client, tag, target string) {
	t.Helper()
	params := map[string]any{"port": dummyMountPort, "tag": tag, "target": target}
	if err := c.Call(context.Background(), "mount", params, nil); err != nil {
		t.Fatalf("guest mount %s at %s: %v", tag, target, err)
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
