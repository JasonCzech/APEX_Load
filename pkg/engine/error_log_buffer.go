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
	mu             sync.Mutex
	records        []bufferedLogRecord
	bytes          int
	limit          int
	classification *RedactionConfig
	omittedRecords int
}
type errorLogHandler struct {
	next           slog.Handler
	buffer         *errorLogBuffer
	redact         func(string) string
	unavailable    func() bool
	classification func() *RedactionConfig
	attrs          []slog.Attr
	group          string
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
	if h.unavailable != nil && h.unavailable() {
		h.buffer.close()
		return nil
	}
	// Freeze and redact before retention: never retain pointers to mutable action
	// results, raw credentials, or pooled response buffers in an error log.
	message := h.redact(r.Message)
	if len(message) > 4096 {
		message = "log detail omitted by retention limit"
	}
	clean := slog.NewRecord(r.Time, r.Level, strings.Clone(message), r.PC)
	size := len(clean.Message) + 256
	count := 0
	add := func(a slog.Attr) {
		if count >= 32 {
			return
		}
		count++
		key := h.redact(h.group + a.Key)
		if len(key) > 256 {
			key = "omitted_detail"
		}
		value := fmt.Sprint(a.Value.Resolve().Any())
		value = h.redact(value)
		if len(value) > 4096 {
			value = "log detail omitted by retention limit"
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
	if h.classification != nil {
		current := h.classification()
		if current != b.classification {
			// Earlier helpers may already have bounded an unknown value to a
			// prefix. A later exact matcher cannot safely recover that spelling.
			// Never flush records across credential-classification epochs.
			b.omittedRecords += len(b.records)
			b.clearLocked()
			b.classification = current
		}
	}
	if r.Level >= slog.LevelError {
		var first error
		if b.omittedRecords > 0 {
			omission := slog.NewRecord(r.Time, slog.LevelInfo, "prior buffered log records omitted after credential classification changed", r.PC)
			omission.AddAttrs(slog.Int("omitted_records", b.omittedRecords))
			first = h.next.Handle(ctx, omission)
			b.omittedRecords = 0
		}
		for _, entry := range b.records {
			// Only records from the current classification epoch remain here.
			projected := slog.NewRecord(entry.record.Time, entry.record.Level, h.redact(entry.record.Message), entry.record.PC)
			entry.record.Attrs(func(attr slog.Attr) bool {
				projected.AddAttrs(slog.String(h.redact(attr.Key), h.redact(attr.Value.String())))
				return true
			})
			if err := h.next.Handle(ctx, projected); err != nil && first == nil {
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
	return slog.New(&errorLogHandler{next: slog.Default().Handler(), buffer: vu.errorLogs, unavailable: func() bool { return vu.resultPublicationUnavailable() }, classification: vu.resultPublicationRedactionConfig, redact: func(s string) string { return scrubSensitiveTextWithConfig(s, vu.resultPublicationRedactionConfig()) }}).With("vuser", vu.ID, "group", vu.Group)
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
	if vu.resultPublicationUnavailable() {
		// Prepared requests remain exact for execution/private diagnosis, but
		// incomplete joined credential knowledge cannot expose their detail.
		action, name = nil, "Concurrent request"
	}
	if !result.Success {
		vu.makeLogger(cfg).Error(message+" failed", actionLogAttrs(name, action, result, cfg, true, vu.resultPublicationRedactionConfig())...)
	} else if cfg.OnErrorOnly || cfg.Level == "extended" {
		vu.makeLogger(cfg).Info(message+" ok", actionLogAttrs(name, action, result, cfg, false, vu.resultPublicationRedactionConfig())...)
	}
}

func (vu *VUser) runtimeLogger() *slog.Logger {
	cfg := dsl.LogConfig{}
	if vu.Script != nil {
		cfg = vu.Script.Config.Log
	}
	return vu.makeLogger(cfg)
}

// Run/lifecycle loggers can outlive the classification state at their creation.
// Admission therefore belongs at Handle, before authored strings reach a sink.
type runtimePublicationLogHandler struct {
	next   slog.Handler
	vu     *VUser
	attrs  []runtimePublicationLogAttr
	groups []string
}
type runtimePublicationLogAttr struct {
	attr   slog.Attr
	groups []string
}

func (h *runtimePublicationLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *runtimePublicationLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.vu.resultPublicationUnavailable() {
		return nil
	}
	config := h.vu.resultPublicationRedactionConfig()
	projectText := func(value string, limit int) string {
		return cloneBoundedResultString(scrubSensitiveTextWithConfig(value, config), limit)
	}
	clean := slog.NewRecord(record.Time, record.Level, projectText(record.Message, 4096), record.PC)
	remaining := 32
	var projectAttr func(slog.Attr, int) slog.Attr
	projectAttr = func(attr slog.Attr, depth int) slog.Attr {
		if remaining <= 0 || depth >= 16 {
			return slog.Attr{}
		}
		remaining--
		attr.Key = projectText(attr.Key, 256)
		attr.Value = attr.Value.Resolve()
		switch attr.Value.Kind() {
		case slog.KindString:
			attr.Value = slog.StringValue(projectText(attr.Value.String(), 4096))
		case slog.KindGroup:
			children := attr.Value.Group()
			out := make([]slog.Attr, 0, min(len(children), remaining))
			for _, child := range children {
				if remaining <= 0 {
					break
				}
				out = append(out, projectAttr(child, depth+1))
			}
			attr.Value = slog.GroupValue(out...)
		case slog.KindAny:
			attr.Value = slog.StringValue(projectText(fmt.Sprint(attr.Value.Any()), 4096))
		}
		return attr
	}
	add := func(attr slog.Attr, groups []string) {
		if remaining <= 0 {
			return
		}
		attr = projectAttr(attr, 0)
		for i := len(groups) - 1; i >= 0; i-- {
			attr = slog.Attr{Key: projectText(groups[i], 256), Value: slog.GroupValue(attr)}
		}
		clean.AddAttrs(attr)
	}
	for _, item := range h.attrs {
		add(item.attr, item.groups)
	}
	record.Attrs(func(attr slog.Attr) bool { add(attr, h.groups); return remaining > 0 })
	return h.next.Handle(ctx, clean)
}
func (h *runtimePublicationLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]runtimePublicationLogAttr(nil), h.attrs...)
	for _, attr := range attrs {
		if len(c.attrs) >= 32 {
			break
		}
		c.attrs = append(c.attrs, runtimePublicationLogAttr{attr: attr, groups: h.groups})
	}
	return &c
}
func (h *runtimePublicationLogHandler) WithGroup(name string) slog.Handler {
	c := *h
	if name != "" && len(h.groups) < 16 {
		c.groups = append(append([]string(nil), h.groups...), name)
	}
	return &c
}
