//go:build !windows

package vm

import (
	"context"
	"net"

	"github.com/jlagedo/atelier/services/internal/hcs"
)

// DialGuest is unsupported off Windows (no HCS / hvsock).
func (m *Manager) DialGuest(context.Context, string) (net.Conn, error) {
	return nil, hcs.ErrUnsupported
}
