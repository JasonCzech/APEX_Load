package recordbridge

import (
	"context"
	"errors"
	"io"
	"sync"
)

var (
	ErrRetainedReplayNotFound = errors.New("retained replay not found")
	ErrRetainedReplayBusy     = errors.New("retained replay read capacity unavailable")
)

// RetainedReplayReader is supplied by a host with project authorization and
// exact receipt/byte verification. Empty project values never grant global
// access. A successful HAR transfers private reader ownership to the caller.
type RetainedReplayReader interface {
	Session(context.Context, string, string) (*BrowserReplaySession, error)
	HAR(context.Context, string, string) (*BrowserReplaySession, ReplayHAR, error)
}

// NewVerifiedReplayHAR transfers a verified native reader to the HTTP adapter.
// The host must check exact length/hash and authorization before calling this.
// Invalid input leaves ownership with the caller; successful Close is idempotent.
func NewVerifiedReplayHAR(reader io.Reader, size int64, closeReader func()) (ReplayHAR, error) {
	if reader == nil || size <= 0 || size > 256<<20 || closeReader == nil {
		return ReplayHAR{}, ErrRetainedReplayUnavailable
	}
	var once sync.Once
	return ReplayHAR{stream: reader, size: size, Fidelity: "native", closeStream: func() { once.Do(closeReader) }}, nil
}
