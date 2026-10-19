// Package testlog swaps the process-wide slog default for one test and puts
// everything it changed back when the test ends.
package testlog

import (
	"log"
	"log/slog"
	"testing"
)

// SetDefault makes logger the slog default until t ends. slog.SetDefault also
// points the log package at logger's handler, and putting the previous slog
// default back does not undo that: the log package would keep writing into
// this test's handler, and so would the restored default slog logger, which
// writes through the log package. The log package's writer and flags are
// therefore restored too. The default is process-wide, so a test that calls
// SetDefault must not run in parallel with tests that log.
func SetDefault(t testing.TB, logger *slog.Logger) {
	t.Helper()
	previous, writer, flags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(logger)
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(writer)
		log.SetFlags(flags)
	})
}
