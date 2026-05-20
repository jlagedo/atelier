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
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jlagedo/atelier/services/internal/rpc"
)

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
	_ = fs.Parse(args)

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
	case "startVM", "stopVM":
		params = map[string]any{"id": *id}
	}

	conn, err := rpc.Dial(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", *addr, err)
		os.Exit(1)
	}
	client := rpc.NewClient(conn)
	defer client.Close()

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
