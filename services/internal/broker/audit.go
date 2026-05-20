package broker

import (
	"io"
	"log/slog"
)

// NewAuditLogger returns a structured (JSON) logger for the audit trail —
// who/what/when/which door (design.md §10).
func NewAuditLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
