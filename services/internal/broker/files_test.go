package broker

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestJailPath is the Files-door security crux (design.md §10): a request path is
// resolved against the workspace root and must stay inside it — ".." escapes,
// absolute paths, and escaping symlinks are all rejected.
func TestJailPath(t *testing.T) {
	root, err := canonicalRoot(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalRoot: %v", err)
	}

	cases := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"simple file", "orders.csv", false},
		{"nested", "sub/dir/file.txt", false},
		{"forward slashes", "a/b/c", false},
		{"dot-dot escape", "../escape", true},
		{"buried dot-dot escape", "a/../../escape", true},
		{"absolute unix", "/etc/passwd", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jailPath(root, tc.rel)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("jailPath(%q) = %q, want error", tc.rel, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("jailPath(%q): unexpected error: %v", tc.rel, err)
			}
			if !withinRoot(root, got) {
				t.Fatalf("jailPath(%q) = %q, outside root %q", tc.rel, got, root)
			}
		})
	}
}

// TestJailPathClosedDoor: with no workspace root configured the door is closed.
func TestJailPathClosedDoor(t *testing.T) {
	if _, err := jailPath("", "anything"); err == nil {
		t.Fatal("jailPath with empty root: want error (door closed)")
	}
}

// TestJailPathSymlinkEscape: a symlink inside the workspace that points outside
// it must be rejected (the escaping-symlink case in §10).
func TestJailPathSymlinkEscape(t *testing.T) {
	root, err := canonicalRoot(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalRoot: %v", err)
	}
	outside := t.TempDir() // a separate tree
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create symlink (insufficient privilege?): %v", err)
	}
	if _, err := jailPath(root, "escape/secret.txt"); err == nil {
		t.Fatal("jailPath through escaping symlink: want error")
	}
	// A symlink that stays inside the workspace is fine.
	innerTarget := filepath.Join(root, "real")
	if err := os.Mkdir(innerTarget, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	innerLink := filepath.Join(root, "innerlink")
	if err := os.Symlink(innerTarget, innerLink); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if _, err := jailPath(root, "innerlink/ok.txt"); err != nil {
		t.Fatalf("jailPath through in-workspace symlink: unexpected error: %v", err)
	}
}

// TestReadWriteRoundTrip exercises the host-side Files door end to end (no VM):
// writeFile then readFile returns the same bytes, binary content survives base64,
// and a "../" path is denied.
func TestReadWriteRoundTrip(t *testing.T) {
	ws := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(log, nil)
	root, err := canonicalRoot(ws)
	if err != nil {
		t.Fatalf("canonicalRoot: %v", err)
	}
	b.setWorkspace(root) // stand in for attachWorkspace (no VM in this unit test)

	payload := []byte{0x00, 0x01, 0xff, 0xfe, 'h', 'i', '\n', 0x80}
	wp, _ := json.Marshal(WriteFileParams{
		Path:    "out.bin",
		Content: base64.StdEncoding.EncodeToString(payload),
	})
	if _, err := b.writeFile(t.Context(), wp); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	rp, _ := json.Marshal(ReadFileParams{Path: "out.bin"})
	res, err := b.readFile(t.Context(), rp)
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	fc := res.(FileContent)
	if fc.Size != len(payload) {
		t.Errorf("size = %d, want %d", fc.Size, len(payload))
	}
	got, err := base64.StdEncoding.DecodeString(fc.Content)
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("round-trip mismatch: got %v want %v", got, payload)
	}

	// The bytes really landed on disk inside the workspace.
	onDisk, err := os.ReadFile(filepath.Join(ws, "out.bin"))
	if err != nil || string(onDisk) != string(payload) {
		t.Errorf("on-disk content mismatch: %v / %v", onDisk, err)
	}

	// A traversal path is denied.
	bad, _ := json.Marshal(ReadFileParams{Path: "../escape"})
	if _, err := b.readFile(t.Context(), bad); err == nil {
		t.Error("readFile ../escape: want error")
	}
}

// TestFilesDoorClosed: with no workspace, readFile/writeFile return a clean error.
func TestFilesDoorClosed(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(log, nil) // no workspace attached → door closed

	rp, _ := json.Marshal(ReadFileParams{Path: "x"})
	if _, err := b.readFile(t.Context(), rp); err == nil {
		t.Error("readFile with closed door: want error")
	}
}
