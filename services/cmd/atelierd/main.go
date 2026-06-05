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
	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run owns the broker lifecycle and returns an error instead of calling os.Exit,
// so the deferred cleanup (listener close, signal stop) runs on every exit path.
func run() error {
	addr := flag.String("addr", rpc.DefaultAddress, "listen address (named pipe on windows, unix socket otherwise)")
	flag.Parse()

	log := broker.NewAuditLogger(os.Stderr)

	ln, err := rpc.Listen(*addr)
	if err != nil {
		log.Error("listen", "addr", *addr, "err", err)
		return err
	}
	defer func() { _ = ln.Close() }()

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
		return err
	}
	log.Info("atelierd stopped")
	return nil
}
