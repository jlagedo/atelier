//go:build !linux

// Stub so `go build ./...` stays green on the macOS/Windows dev hosts. The shim only ever
// runs inside the Linux guest VM.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "atelier-landlock: Linux only")
	os.Exit(1)
}
