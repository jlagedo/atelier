// Command atelierd is the privileged broker service. On Windows it runs as a
// LocalSystem service exposing a named pipe (design.md §9); on other platforms it
// runs over a unix socket for terminal-driven development.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"

	"github.com/jlagedo/atelier/services/internal/broker"
	"github.com/jlagedo/atelier/services/internal/rpc"
)

func main() {
	addr := flag.String("addr", rpc.DefaultAddress, "listen address (named pipe on windows, unix socket otherwise)")
	flag.Parse()

	log := broker.NewAuditLogger(os.Stderr)

	ln, err := rpc.Listen(*addr)
	if err != nil {
		log.Error("listen", "addr", *addr, "err", err)
		os.Exit(1)
	}
	defer ln.Close()

	srv := rpc.NewServer(log)
	broker.New(log, nil).Register(srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	log.Info("atelierd listening", "addr", *addr)
	if err := srv.Serve(ctx, ln); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
	log.Info("atelierd stopped")
}
