// Command vmctl is a dev CLI that drives the host from a terminal — no Electron
// needed (design.md §8, M0-M2). It sends one JSON-RPC call over Hop 2 and prints
// the result.
//
// Usage:
//
//	vmctl [method] [flags]
//
//	vmctl getStatus
//	vmctl createVM -id vm0 -kernel C:\path\vmlinuz -rootfs E:\path\rootfs.vhd [-initrd C:\path\initrd -mem 2048 -cpu 2]
//	vmctl startVM  -id vm0
//	vmctl stopVM   -id vm0
//	vmctl exec     -id vm0 [-cwd /tmp] [-env K=V ...] -- ls -la /
//	vmctl attachWorkspace -id vm0 -path E:\path\folder   (share folder at /workspace)
//	vmctl detachWorkspace -id vm0
//	vmctl readFile  -path notes.txt                      (prints to stdout)
//	vmctl writeFile -path out.txt [-content "..."]       (else reads stdin)
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
	mem := fs.Uint64("mem", 0, "memory in MB (0 = broker default)")
	cpu := fs.Int("cpu", 0, "processor count (0 = broker default)")
	cwd := fs.String("cwd", "", "working directory in the guest (exec)")
	env := envFlag{}
	fs.Var(env, "env", "guest env var KEY=VALUE (exec; repeatable)")
	path := fs.String("path", "", "workspace-relative file path (readFile/writeFile)")
	content := fs.String("content", "", "file content (writeFile; if unset, read from stdin)")
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
	// (use "--" to separate, e.g. `vmctl exec -id vm0 -- ls -la /`).
	if method == "exec" {
		cmdv := fs.Args()
		if len(cmdv) == 0 {
			fmt.Fprintln(os.Stderr, "exec: missing command (usage: vmctl exec -id vm0 -- cmd args...)")
			os.Exit(2)
		}
		params := map[string]any{
			"id":   *id,
			"cmd":  cmdv[0],
			"args": cmdv[1:],
			"cwd":  *cwd,
			"env":  map[string]string(env),
		}
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
			os.Exit(1)
		}
		os.Exit(res.ExitCode)
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
			"id":         *id,
			"kernelPath": *kernel,
			"initrdPath": *initrd,
			"rootfsPath": *rootfs,
			"memoryMB":   *mem,
			"cpuCount":   *cpu,
		}
	case "startVM", "stopVM", "detachWorkspace":
		params = map[string]any{"id": *id}
	case "attachWorkspace":
		params = map[string]any{"id": *id, "path": *path}
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
