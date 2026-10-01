package analysis

import (
	"context"
	"errors"

	"github.com/apache/thrift/lib/go/thrift"
)

const maxParquetMetadataItems = 65536
const maxParquetMetadataDepth = 64

// Generated Thrift readers allocate collections directly from wire counts.
// Limit counts before those allocations, including unknown fields and nesting.
// The transport is separately limited to the already validated footer bytes.
type boundedMetadataProtocol struct {
	thrift.TProtocol
	remaining int
	depth     int
}

func (p *boundedMetadataProtocol) reserve(n int) error {
	if n < 0 || n > maxParquetSchemaElements || n > p.remaining {
		return errors.New("Parquet metadata collection limit exceeded")
	}
	p.remaining -= n
	return nil
}

func (p *boundedMetadataProtocol) ReadListBegin(ctx context.Context) (thrift.TType, int, error) {
	t, n, err := p.TProtocol.ReadListBegin(ctx)
	if err == nil {
		err = p.reserve(n)
	}
	return t, n, err
}

func (p *boundedMetadataProtocol) ReadSetBegin(ctx context.Context) (thrift.TType, int, error) {
	t, n, err := p.TProtocol.ReadSetBegin(ctx)
	if err == nil {
		err = p.reserve(n)
	}
	return t, n, err
}

func (p *boundedMetadataProtocol) ReadMapBegin(ctx context.Context) (thrift.TType, thrift.TType, int, error) {
	k, v, n, err := p.TProtocol.ReadMapBegin(ctx)
	if err == nil {
		err = p.reserve(n)
	}
	if err == nil {
		err = p.reserve(n)
	}
	return k, v, n, err
}

func (p *boundedMetadataProtocol) ReadStructBegin(ctx context.Context) (string, error) {
	if p.depth >= maxParquetMetadataDepth {
		return "", errors.New("Parquet metadata nesting limit exceeded")
	}
	if err := p.reserve(1); err != nil {
		return "", err
	}
	p.depth++
	return p.TProtocol.ReadStructBegin(ctx)
}

func (p *boundedMetadataProtocol) ReadStructEnd(ctx context.Context) error {
	p.depth--
	return p.TProtocol.ReadStructEnd(ctx)
}

func (p *boundedMetadataProtocol) Skip(ctx context.Context, t thrift.TType) error {
	// Use this wrapper during skips so unknown fields cannot bypass limits.
	return thrift.Skip(ctx, p, t, maxParquetMetadataDepth)
}
