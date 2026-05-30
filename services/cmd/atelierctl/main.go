// Command atelierctl is a dev CLI that drives the host from a terminal — no Electron
// needed (design.md §8, M0-M2). It sends one JSON-RPC call over Hop 2 and prints
// the result.
//
// Usage:
//
//	atelierctl [method] [flags]
//
//	atelierctl getStatus
//	atelierctl createVM -id vm0 -kernel C:\path\vmlinuz -rootfs E:\path\rootfs.vhd [-initrd C:\path\initrd -runner B\runner.img -mem 2048 -cpu 2]
//	atelierctl startVM  -id vm0
//	atelierctl stopVM   -id vm0
//	atelierctl exec     -id vm0 [-cwd /tmp] [-env K=V ...] [-session s1] -- ls -la /
//	atelierctl execInput -id vm0 -session s1 [-content "..."]  (else reads stdin; feeds the session's stdin)
//	atelierctl attachWorkspace -id vm0 -path E:\path\folder   (share folder at /workspace)
//	atelierctl detachWorkspace -id vm0
//	atelierctl readFile  -path notes.txt                      (prints to stdout)
//	atelierctl writeFile -path out.txt [-content "..."]       (else reads stdin)
//	atelierctl setEgressPolicy -allow pypi.org,files.pythonhosted.org  (empty = deny all)
//	atelierctl setTime  -id vm0                               (push host wall clock into the guest)
//	atelierctl agent    -id vm0 -- "<task>"   (S5b.1: run the agent loop INSIDE the guest)
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/jlagedo/atelier/services/internal/rpc"
)

// envFlag collects repeated -env KEY=VALUE flags into a map.
type envFlag map[string]string

func (e envFlag) String() string { return "" }

func (e envFlag) Set(kv string) error {
	i := strings.IndexByte(kv, '=')
	if i < 0 {
		return fmt.Errorf("env must be KEY=VALUE, got %q", kv)
	}
	e[kv[:i]] = kv[i+1:]
	return nil
}

// egressHostFromURL returns the hostname of a provider base_url to add to the egress
// allowlist, or "" when unset, unparseable, or loopback (loopback isn't reachable or
// DNS-pinnable from the isolated guest). Mirrors manager.ts baseUrlEgressHosts.
func egressHostFromURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	if h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return ""
	}
	return h
}

// execStream runs a guest `exec` over the broker, relaying the streamed
// stdout/stderr notifications to our own stdout/stderr, and returns the guest's
// exit code. Shared by the `exec` and `agent` subcommands.
func execStream(client *rpc.Client, params map[string]any) int {
	onNotify := func(m string, raw json.RawMessage) {
		if m != "exec/output" {
			return
		}
		var o struct {
			Stream string `json:"stream"`
			Data   string `json:"data"`
		}
		if json.Unmarshal(raw, &o) != nil {
			return
		}
		data, err := base64.StdEncoding.DecodeString(o.Data)
		if err != nil {
			return
		}
		w := os.Stdout
		if o.Stream == "stderr" {
			w = os.Stderr
		}
		_, _ = w.Write(data)
	}
	var res struct {
		ExitCode int `json:"exitCode"`
	}
	if err := client.CallStream(context.Background(), "exec", params, &res, onNotify); err != nil {
		fmt.Fprintf(os.Stderr, "exec: %v\n", err)
		return 1
	}
	return res.ExitCode
}

func main() {
	args := os.Args[1:]
	method := "getStatus"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		method, args = args[0], args[1:]
	}

	fs := flag.NewFlagSet(method, flag.ExitOnError)
	addr := fs.String("addr", rpc.DefaultAddress, "host address")
	id := fs.String("id", "vm0", "vm id")
	kernel := fs.String("kernel", "", "host path to a direct-boot kernel")
	initrd := fs.String("initrd", "", "host path to the boot initrd (optional)")
	rootfs := fs.String("rootfs", "", "host path to the rootfs VHD")
	runner := fs.String("runner", "", "host path to the runner volume image (darwin/VZ; attached ro as a second disk)")
	mem := fs.Uint64("mem", 0, "memory in MB (0 = broker default)")
	cpu := fs.Int("cpu", 0, "processor count (0 = broker default)")
	cwd := fs.String("cwd", "", "working directory in the guest (exec)")
	env := envFlag{}
	fs.Var(env, "env", "guest env var KEY=VALUE (exec; repeatable)")
	path := fs.String("path", "", "workspace-relative file path (readFile/writeFile)")
	content := fs.String("content", "", "file content (writeFile; if unset, read from stdin)")
	allow := fs.String("allow", "", "comma-separated egress allowlist host suffixes (setEgressPolicy; empty = deny all)")
	target := fs.String("target", "", "guest mount path for a per-session share (attachWorkspace; e.g. /sessions/a)")
	tag := fs.String("tag", "", "9p share tag/name for a per-session share (attach/detachWorkspace)")
	wsport := fs.Uint64("wsport", 0, "vsock port for a per-session share (attachWorkspace; 0 = broker allocates)")
	session := fs.String("session", "", "exec session id: registers a stdin channel (exec) / targets one (execInput)")
	_ = fs.Parse(args)

	conn, err := rpc.Dial(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", *addr, err)
		os.Exit(1)
	}
	client := rpc.NewClient(conn)
	defer client.Close()

	// exec streams the guest's stdout/stderr back as notifications, then returns
	// an exit code we propagate. The command vector is everything after the flags
	// (use "--" to separate, e.g. `atelierctl exec -id vm0 -- ls -la /`).
	if method == "exec" {
		cmdv := fs.Args()
		if len(cmdv) == 0 {
			fmt.Fprintln(os.Stderr, "exec: missing command (usage: atelierctl exec -id vm0 -- cmd args...)")
			os.Exit(2)
		}
		params := map[string]any{
			"id":        *id,
			"cmd":       cmdv[0],
			"sessionId": *session,
			"args":      cmdv[1:],
			"cwd":       *cwd,
			"env":       map[string]string(env),
		}
		os.Exit(execStream(client, params))
	}

	// execInput feeds a chunk into a persistent exec session's stdin (S6.1). Data
	// comes from -content or stdin and travels base64-encoded.
	if method == "execInput" {
		var raw []byte
		if *content != "" {
			raw = []byte(*content)
		} else {
			var err error
			if raw, err = io.ReadAll(os.Stdin); err != nil {
				fmt.Fprintf(os.Stderr, "execInput: read stdin: %v\n", err)
				os.Exit(1)
			}
		}
		p := map[string]any{"id": *id, "sessionId": *session, "data": base64.StdEncoding.EncodeToString(raw)}
		var result json.RawMessage
		if err := client.Call(context.Background(), "execInput", p, &result); err != nil {
			fmt.Fprintf(os.Stderr, "execInput: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("ok (execInput %s)\n", *session)
		return
	}

	// agent (S5b.1) runs the agent loop INSIDE the guest (Topology B). We open
	// egress to the model host + pip/npm registries (default; -allow overrides), then
	// exec the in-guest agent CLI shipped on the runner volume at /opt/atelier. The loop's
	// tools are the SDK's built-ins acting on the guest fs; only the model call
	// leaves the cage. The API key rides in via the exec env (the operator's env);
	// telemetry/autoupdate are disabled so the allowlist can stay tight.
	if method == "agent" {
		task := strings.TrimSpace(strings.Join(fs.Args(), " "))
		if task == "" {
			fmt.Fprintln(os.Stderr, `agent: missing task (usage: atelierctl agent -id vm0 -- "<task>")`)
			os.Exit(2)
		}
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			fmt.Fprintln(os.Stderr, "agent: ANTHROPIC_API_KEY is not set in this shell's environment")
			os.Exit(1)
		}

		allowList := []string{"api.anthropic.com", "pypi.org", "files.pythonhosted.org", "registry.npmjs.org"}
		// partisan may target a non-anthropic provider via a custom base_url; allow its
		// hostname so the default-deny jail still reaches the model (loopback skipped — not
		// reachable/DNS-pinnable from the guest). Mirrors manager.ts baseUrlEgressHosts.
		base := os.Getenv("ANTHROPIC_BASE_URL")
		if base == "" {
			base = os.Getenv("LLM_BASE_URL")
		}
		if h := egressHostFromURL(base); h != "" {
			allowList = append(allowList, h)
		}
		if strings.TrimSpace(*allow) != "" {
			allowList = nil
			for _, h := range strings.Split(*allow, ",") {
				if h = strings.TrimSpace(h); h != "" {
					allowList = append(allowList, h)
				}
			}
		}
		var egRes json.RawMessage
		if err := client.Call(context.Background(), "setEgressPolicy", map[string]any{"allow": allowList}, &egRes); err != nil {
			fmt.Fprintf(os.Stderr, "agent: setEgressPolicy: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[agent] egress allowlist: %s\n", strings.Join(allowList, ", "))

		// Seed the guest clock right before the loop: the model call does TLS, which
		// fails ("cert not yet valid") if the guest is still at 1970 (no RTC under VZ).
		// Hard guarantee on top of the manager's boot/30s resync.
		var tRes json.RawMessage
		if err := client.Call(context.Background(), "setTime", map[string]any{"id": *id}, &tRes); err != nil {
			fmt.Fprintf(os.Stderr, "agent: setTime: %v\n", err)
			os.Exit(1)
		}

		genv := map[string]string{
			"ANTHROPIC_API_KEY":       apiKey,
			"DISABLE_AUTOUPDATER":     "1",
			"DISABLE_TELEMETRY":       "1",
			"DISABLE_ERROR_REPORTING": "1",
			// partisan keeps stdout NDJSON-only by suppressing the OpenHands SDK banner.
			"OPENHANDS_SUPPRESS_BANNER": "1",
			// Use LiteLLM's bundled cost map; the egress jail blocks its GitHub fetch (metadata
			// only — the call routes by the explicit anthropic/ provider regardless).
			"LITELLM_LOCAL_MODEL_COST_MAP": "True",
			// Non-root agent (CRIT-01): HOME/TMPDIR/cache must point at writable tmpfs
			// paths — the agent runs as uid 1001 and /opt is read-only. /home/atelier is a
			// tmpfs owned by 1001 and /tmp is a tmpfs (image/guest/init.sh). PARTISAN_PERSIST
			// (writable too) holds the OpenHands conversation store for --resume.
			"HOME":             "/home/atelier",
			"TMPDIR":           "/tmp",
			"XDG_CACHE_HOME":   "/home/atelier/.cache",
			"PARTISAN_PERSIST": "/home/atelier/.partisan",
		}
		// Forward provider knobs if set (model override, base URL).
		for _, k := range []string{"ATELIER_MODEL", "ANTHROPIC_BASE_URL"} {
			if v := os.Getenv(k); v != "" {
				genv[k] = v
			}
		}
		// Any explicit -env overrides win.
		for k, v := range env {
			genv[k] = v
		}

		params := map[string]any{
			"id":   *id,
			"cmd":  "/opt/atelier/packages/partisan/.venv/bin/python",
			"args": []string{"cli_guest.py", "--task", task},
			"cwd":  "/opt/atelier/packages/partisan",
			"env":  genv,
		}
		os.Exit(execStream(client, params))
	}

	// Files door (design.md §10): content travels base64-encoded so binary files
	// (Excel/Word/PDF) survive the JSON wire. readFile decodes to stdout;
	// writeFile encodes from -content or stdin.
	if method == "readFile" {
		var res struct {
			Content string `json:"content"`
		}
		if err := client.Call(context.Background(), "readFile", map[string]any{"path": *path}, &res); err != nil {
			fmt.Fprintf(os.Stderr, "readFile: %v\n", err)
			os.Exit(1)
		}
		data, err := base64.StdEncoding.DecodeString(res.Content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "readFile: bad content encoding: %v\n", err)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(data)
		return
	}
	if method == "writeFile" {
		var raw []byte
		if *content != "" {
			raw = []byte(*content)
		} else {
			var err error
			if raw, err = io.ReadAll(os.Stdin); err != nil {
				fmt.Fprintf(os.Stderr, "writeFile: read stdin: %v\n", err)
				os.Exit(1)
			}
		}
		p := map[string]any{"path": *path, "content": base64.StdEncoding.EncodeToString(raw)}
		var result json.RawMessage
		if err := client.Call(context.Background(), "writeFile", p, &result); err != nil {
			fmt.Fprintf(os.Stderr, "writeFile: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("ok (writeFile %s)\n", *path)
		return
	}

	var params any
	switch method {
	case "createVM":
		params = map[string]any{
			"id":              *id,
			"kernelPath":      *kernel,
			"initrdPath":      *initrd,
			"rootfsPath":      *rootfs,
			"runnerImagePath": *runner,
			"memoryMB":        *mem,
			"cpuCount":        *cpu,
		}
	case "startVM", "stopVM", "setTime":
		params = map[string]any{"id": *id}
	case "detachWorkspace":
		params = map[string]any{"id": *id, "tag": *tag}
	case "attachWorkspace":
		params = map[string]any{"id": *id, "path": *path, "target": *target, "tag": *tag, "port": *wsport}
	case "setEgressPolicy":
		var allowList []string
		for _, h := range strings.Split(*allow, ",") {
			if h = strings.TrimSpace(h); h != "" {
				allowList = append(allowList, h)
			}
		}
		params = map[string]any{"allow": allowList}
	}

	var result json.RawMessage
	if err := client.Call(context.Background(), method, params, &result); err != nil {
		fmt.Fprintf(os.Stderr, "call %s: %v\n", method, err)
		os.Exit(1)
	}
	if len(result) == 0 || string(result) == "null" {
		fmt.Printf("ok (%s)\n", method)
		return
	}
	fmt.Println(string(result))
}
