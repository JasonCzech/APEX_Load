package eventhubscapture

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/hamba/avro/v2"
	"github.com/hamba/avro/v2/ocf"
	"github.com/klauspost/compress/zstd"
)

// An uploaded container is size-limited, but hamba trusts its framing: it
// allocates each block's declared length and that block's whole decompressed
// output before decoding, and grows an array to its declared block count before
// reading an element. A few crafted bytes could otherwise demand terabytes. The
// container is therefore scanned first and only blocks within these bounds are
// handed to hamba.
var (
	// maxDecodedBytes bounds the decompressed size of the blocks that are
	// actually decoded (those holding the first max_events records).
	maxDecodedBytes int64 = 256 << 20
	// maxArrayElements bounds a single array's declared element count.
	maxArrayElements = 1 << 20
)

var ocfMagic = []byte{'O', 'b', 'j', 1}

const ocfSyncSize = 16

// container is an ocf.Decoder over the bounded part of an upload.
type container struct {
	*ocf.Decoder
	// records is the record count the block headers declare.
	records int
	// problem explains why the container was cut short, when it was.
	problem string
}

func openContainer(data []byte, maxEvents int) (*container, error) {
	scan, err := scanContainer(data, maxEvents)
	if err != nil {
		return nil, err
	}
	dec, err := ocf.NewDecoder(bytes.NewReader(data[:scan.usable]),
		ocf.WithDecoderConfig(avro.Config{MaxSliceAllocSize: maxArrayElements}.Freeze()),
		ocf.WithZStandardDecoderOptions(zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(uint64(maxDecodedBytes))),
	)
	if err != nil {
		return nil, err
	}
	return &container{Decoder: dec, records: scan.records, problem: scan.problem}, nil
}

type containerScan struct {
	usable  int // bytes of data hamba may read: the header plus every complete, bounded block
	records int
	problem string
}

// scanContainer walks the Object Container framing without decompressing past
// what will be decoded. It counts records from the block headers and stops at
// the first block that is malformed, truncated, or would decompress beyond
// maxDecodedBytes, so the records before it still import.
func scanContainer(data []byte, maxEvents int) (containerScan, error) {
	if !bytes.HasPrefix(data, ocfMagic) {
		// Not a container; let hamba report the format error.
		return containerScan{usable: len(data)}, nil
	}
	r := ocfReader{data: data, pos: len(ocfMagic)}
	codec, err := r.header()
	if err != nil {
		return containerScan{}, err
	}
	scan := containerScan{usable: r.pos}
	var decoded int64
	for r.pos < len(data) {
		start := r.pos
		count, countErr := r.long()
		size, sizeErr := r.long()
		switch {
		case countErr != nil || sizeErr != nil:
			scan.problem = fmt.Sprintf("Avro file is truncated inside the block header at byte %d; read the complete blocks before it", start)
			return scan, nil
		case count < 0 || size < 0:
			scan.problem = fmt.Sprintf("Avro block at byte %d declares a negative record count or size; read the blocks before it", start)
			return scan, nil
		case size > int64(len(data)-r.pos-ocfSyncSize):
			scan.problem = fmt.Sprintf("Avro file is truncated: the block at byte %d declares %d bytes but only %d remain; read the complete blocks before it", start, size, max(0, len(data)-r.pos-ocfSyncSize))
			return scan, nil
		case count == 0:
			// hamba ends the stream at an empty block; nothing after it is read.
			return scan, nil
		case count > int64(math.MaxInt32)-int64(scan.records):
			scan.problem = fmt.Sprintf("Avro block at byte %d declares %d records, more than an import can count; read the blocks before it", start, count)
			return scan, nil
		}
		block := data[r.pos : r.pos+int(size)]
		if scan.records < maxEvents {
			n, err := decodedBlockSize(codec, block, maxDecodedBytes-decoded)
			if err != nil {
				scan.problem = fmt.Sprintf("Avro block at byte %d %v; read the blocks before it", start, err)
				return scan, nil
			}
			decoded += n
		}
		r.pos += int(size) + ocfSyncSize
		scan.usable = r.pos
		scan.records += int(count)
	}
	return scan, nil
}

// decodedBlockSize measures a block's decompressed size without holding it, and
// fails once it exceeds limit. Corrupt blocks are measured as far as they go;
// hamba reports the corruption when it decodes them.
func decodedBlockSize(codec string, block []byte, limit int64) (int64, error) {
	tooLarge := fmt.Errorf("decompresses beyond the %s import limit", formatByteLimit(maxDecodedBytes))
	switch ocf.CodecName(codec) {
	case ocf.Null, "":
		if int64(len(block)) > limit {
			return 0, tooLarge
		}
		return int64(len(block)), nil
	case ocf.Deflate:
		reader := flate.NewReader(bytes.NewReader(block))
		defer func() { _ = reader.Close() }()
		n, _ := io.CopyN(io.Discard, reader, limit+1)
		if n > limit {
			return 0, tooLarge
		}
		return n, nil
	case ocf.Snappy:
		// hamba's snappy blocks are a snappy block plus a 4-byte CRC; the snappy
		// block starts with its decoded length, which hamba allocates up front.
		if len(block) < 4 {
			return 0, nil
		}
		decodedLen, read := binary.Uvarint(block[:len(block)-4])
		if read <= 0 {
			return 0, nil
		}
		if decodedLen > uint64(limit) {
			return 0, tooLarge
		}
		return int64(decodedLen), nil
	case ocf.ZStandard:
		// hamba decodes the whole block with DecodeAll, which preallocates the
		// frame's declared content size; the decoder option bounds that too.
		var header zstd.Header
		if header.Decode(block) == nil && header.HasFCS && header.FrameContentSize > uint64(limit) {
			return 0, tooLarge
		}
		reader, err := zstd.NewReader(bytes.NewReader(block), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(uint64(maxDecodedBytes)))
		if err != nil {
			return 0, nil
		}
		defer reader.Close()
		n, _ := io.CopyN(io.Discard, reader, limit+1)
		if n > limit {
			return 0, tooLarge
		}
		return n, nil
	default:
		// hamba rejects an unknown codec before reading any block.
		return 0, nil
	}
}

// ocfReader reads the zig-zag varints and length-prefixed bytes of the
// container framing.
type ocfReader struct {
	data []byte
	pos  int
}

var errTruncated = errors.New("truncated Avro container")

func (r *ocfReader) long() (int64, error) {
	var (
		value uint64
		shift uint
	)
	for {
		if r.pos >= len(r.data) || shift >= 64 {
			return 0, errTruncated
		}
		b := r.data[r.pos]
		r.pos++
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return int64(value>>1) ^ -int64(value&1), nil
		}
		shift += 7
	}
}

func (r *ocfReader) bytes() ([]byte, error) {
	n, err := r.long()
	if err != nil {
		return nil, err
	}
	if n < 0 || n > int64(len(r.data)-r.pos) {
		return nil, errTruncated
	}
	out := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

// header reads the metadata map and sync marker that follow the magic and
// returns the declared codec.
func (r *ocfReader) header() (string, error) {
	codec := ""
	for {
		count, err := r.long()
		if err != nil {
			return "", err
		}
		if count == 0 {
			break
		}
		if count < 0 {
			// A negative map block count is followed by the block's byte size.
			count = -count
			if _, err := r.long(); err != nil {
				return "", err
			}
		}
		for range count {
			key, err := r.bytes()
			if err != nil {
				return "", err
			}
			value, err := r.bytes()
			if err != nil {
				return "", err
			}
			if string(key) == "avro.codec" {
				codec = string(value)
			}
		}
	}
	if len(r.data)-r.pos < ocfSyncSize {
		return "", errTruncated
	}
	r.pos += ocfSyncSize
	return codec, nil
}

func formatByteLimit(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	default:
		return fmt.Sprintf("%d-byte", n)
	}
}
