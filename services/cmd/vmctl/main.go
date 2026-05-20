// Command vmctl is a dev CLI that drives the host from a terminal — no Electron
// needed (design.md §8, M0-M2). It sends one JSON-RPC call over Hop 2 and prints
// the result. Usage: vmctl [-addr ADDR] [method]   (default method: getStatus)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jlagedo/atelier/services/internal/rpc"
)

func main() {
	addr := flag.String("addr", rpc.DefaultAddress, "host address")
	flag.Parse()

	method := "getStatus"
	if args := flag.Args(); len(args) > 0 {
		method = args[0]
	}

	conn, err := rpc.Dial(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", *addr, err)
		os.Exit(1)
	}
	client := rpc.NewClient(conn)
	defer client.Close()

	var result json.RawMessage
	if err := client.Call(context.Background(), method, nil, &result); err != nil {
		fmt.Fprintf(os.Stderr, "call %s: %v\n", method, err)
		os.Exit(1)
	}
	fmt.Println(string(result))
}
