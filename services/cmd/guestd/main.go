// Command guestd is the in-VM daemon: the hvsocket RPC server side of the host's
// control plane (design.md §8 Hop 3, M5b). Scaffold only — not implemented yet.
package main

import (
	"flag"
	"log/slog"
	"os"
)

func main() {
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	log.Info("atelier-guestd scaffold — hvsocket RPC server not implemented yet (M5b)")
}
