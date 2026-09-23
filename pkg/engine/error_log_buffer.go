package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxErrorLogProcessBytes = 16 << 20

var errorLogProcessBytes atomic.Int64

type bufferedLogRecord struct {
	record slog.Record
	size   int
}
type errorLogBuffer struct {
	mu      sync.Mutex
	records []bufferedLogRecord
	bytes   int
	limit   int
}
type errorLogHandler struct {
	next   slog.Handler
	buffer *errorLogBuffer
	redact func(string) string
	attrs  []slog.Attr
	group  string
}

func (b *errorLogBuffer) clearLocked() {
	errorLogProcessBytes.Add(-int64(b.bytes))
	b.bytes = 0
	b.records = nil
}
func (b *errorLogBuffer) close() { b.mu.Lock(); defer b.mu.Unlock(); b.clearLocked() }
func (h *errorLogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}
func (h *errorLogHandler) Handle(ctx context.Context, r slog.Record) error {
	// Freeze and redact before retention: never retain pointers to mutable action
	// results, raw credentials, or pooled response buffers in an error log.
	message := h.redact(r.Message)
	if len(message) > 4096 {
		message = message[:4096]
	}
	clean := slog.NewRecord(r.Time, r.Level, strings.Clone(message), r.PC)
	size := len(clean.Message) + 256
	count := 0
	add := func(a slog.Attr) {
		if count >= 32 {
			return
		}
		count++
		key := h.group + a.Key
		if len(key) > 256 {
			key = key[:256]
		}
		value := fmt.Sprint(a.Value.Resolve().Any())
		value = h.redact(value)
		if len(value) > 4096 {
			value = value[:4096] + "…"
		}
		clean.AddAttrs(slog.String(strings.Clone(key), strings.Clone(value)))
		size += len(key) + len(value) + 32
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { add(a); return true })
	b := h.buffer
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.Level >= slog.LevelError {
		var first error
		for _, entry := range b.records {
			if err := h.next.Handle(ctx, entry.record); err != nil && first == nil {
				first = err
			}
		}
		b.clearLocked()
		if err := h.next.Handle(ctx, clean); err != nil && first == nil {
			first = err
		}
		return first
	}
	if size > b.limit {
		return nil
	}
	for b.bytes+size > b.limit && len(b.records) > 0 {
		old := b.records[0]
		b.records[0] = bufferedLogRecord{}
		b.records = b.records[1:]
		b.bytes -= old.size
		errorLogProcessBytes.Add(-int64(old.size))
	}
	for {
		used := errorLogProcessBytes.Load()
		if used+int64(size) > maxErrorLogProcessBytes {
			return nil
		}
		if errorLogProcessBytes.CompareAndSwap(used, used+int64(size)) {
			break
		}
	}
	b.records = append(b.records, bufferedLogRecord{clean, size})
	b.bytes += size
	return nil
}
func (h *errorLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &c
}
func (h *errorLogHandler) WithGroup(name string) slog.Handler {
	c := *h
	if name != "" {
		c.group += name + "."
	}
	return &c
}
func (vu *VUser) errorOnlyLogger(cfg dsl.LogConfig) *slog.Logger {
	if vu.errorLogs == nil {
		limit := cfg.ErrorBufferBytes
		if limit == 0 {
			limit = 100 * 1024
		}
		if limit < 0 || limit > 1<<20 {
			limit = 1 << 20
		}
		vu.errorLogs = &errorLogBuffer{limit: limit}
	}
	return slog.New(&errorLogHandler{next: slog.Default().Handler(), buffer: vu.errorLogs, redact: func(s string) string { return scrubSensitiveTextWithConfig(s, vu.RedactionConfig) }}).With("vuser", vu.ID, "group", vu.Group)
}

func (vu *VUser) closeErrorLogs() {
	if vu.errorLogs != nil {
		vu.errorLogs.close()
	}
}

func (vu *VUser) logDiagnosticResult(message, name string, action *protocol.Action, result *protocol.Result) {
	if result == nil || vu.Script == nil {
		return
	}
	cfg := vu.Script.Config.Log
	if !result.Success {
		vu.makeLogger(cfg).Error(message+" failed", actionLogAttrs(name, action, result, cfg, true, vu.RedactionConfig)...)
	} else if cfg.OnErrorOnly || cfg.Level == "extended" {
		vu.makeLogger(cfg).Info(message+" ok", actionLogAttrs(name, action, result, cfg, false, vu.RedactionConfig)...)
	}
}

func (vu *VUser) runtimeLogger() *slog.Logger {
	cfg := dsl.LogConfig{}
	if vu.Script != nil {
		cfg = vu.Script.Config.Log
	}
	return vu.makeLogger(cfg)
}
