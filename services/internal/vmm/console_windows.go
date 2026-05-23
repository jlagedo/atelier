//go:build windows

package vmm

import (
	"bufio"
	"log/slog"
	"net"

	winio "github.com/Microsoft/go-winio"
)

// consolePipeSDDL restricts the serial-console pipe to SYSTEM + Builtin
// Administrators. The VM worker process (vmwp) for a utility VM connects as
// SYSTEM; if HCS can't reach the pipe on some configs, widen this in boot testing.
const consolePipeSDDL = "D:P(A;;FA;;;SY)(A;;FA;;;BA)"

type consoleStream interface{ Close() error }

// winConsole is the host end of a VM's COM1 serial console: a named-pipe server
// HCS connects to. Each connection's bytes are logged line-by-line.
type winConsole struct {
	ln  net.Listener
	log *slog.Logger
}

func newConsoleStream(pipe string, log *slog.Logger) (consoleStream, error) {
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{
		MessageMode:        false,
		SecurityDescriptor: consolePipeSDDL,
	})
	if err != nil {
		return nil, err
	}
	c := &winConsole{ln: ln, log: log}
	go c.serve()
	return c, nil
}

func (c *winConsole) serve() {
	for {
		conn, err := c.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go c.pump(conn)
	}
}

func (c *winConsole) pump(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		c.log.Info("console", "line", sc.Text())
	}
}

func (c *winConsole) Close() error { return c.ln.Close() }
