package eventhubscapture

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/iskorotkov/avro/v2/ocf"
)

// ocfHeader returns the header (magic, metadata, sync marker) of an empty
// container, so a test can append hand-crafted blocks after it.
func ocfHeader(t *testing.T, schema string) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(schema, &buf, ocf.WithCodec(ocf.Null))
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func zigzag(v int64) []byte {
	u := uint64((v << 1) ^ (v >> 63))
	var out []byte
	for u >= 0x80 {
		out = append(out, byte(u)|0x80)
		u >>= 7
	}
	return append(out, byte(u))
}

// appendBlock appends one null-codec block carrying count records.
func appendBlock(container []byte, header []byte, count int64, payload []byte) []byte {
	container = append(container, zigzag(count)...)
	container = append(container, zigzag(int64(len(payload)))...)
	container = append(container, payload...)
	return append(container, header[len(header)-ocfSyncSize:]...)
}

func withMaxDecodedBytes(t *testing.T, limit int64) {
	t.Helper()
	previous := maxDecodedBytes
	maxDecodedBytes = limit
	t.Cleanup(func() { maxDecodedBytes = previous })
}

// hamba allocates a block's declared size before reading it; a 40-byte upload
// declaring a 32 TiB block stalled Convert for seconds and is a fatal OOM on a
// Linux controller. The scan must reject it from the framing alone.
func TestConvertRejectsABlockLargerThanTheFile(t *testing.T) {
	header := ocfHeader(t, `"string"`)
	data := append(append([]byte(nil), header...), zigzag(1)...)
	data = append(data, zigzag(1<<45)...)
	data = append(data, "tiny"...)

	started := time.Now()
	_, err := Convert(ConvertRequest{Data: data})
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Convert took %s; the declared block size was trusted", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "Avro file contained no records: Avro file is truncated") || !strings.Contains(err.Error(), "declares 35184372088832 bytes") {
		t.Fatalf("err = %v, want a truncated-block error", err)
	}
}

func TestConvertImportsTheCompleteBlocksBeforeATruncatedOne(t *testing.T) {
	header := ocfHeader(t, `"string"`)
	data := append([]byte(nil), header...)
	data = appendBlock(data, header, 1, append(zigzag(5), "first"...))
	data = appendBlock(data, header, 1, append(zigzag(6), "second"...))
	data = data[:len(data)-10] // cut the second block short

	res, err := Convert(ConvertRequest{Data: data})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Script.Actions) != 1 || res.Script.Actions[0].Body != "first" || res.Stats.RecordsRead != 1 {
		t.Fatalf("actions = %+v stats = %+v", res.Script.Actions, res.Stats)
	}
	if !hasWarningContaining(res.Warnings, "Avro file is truncated: the block at byte") {
		t.Fatalf("missing truncation warning: %v", res.Warnings)
	}
}

func TestConvertBoundsDecompressedBlocks(t *testing.T) {
	withMaxDecodedBytes(t, 64<<10)
	big := strings.Repeat("a", 200<<10) // compresses to a few hundred bytes
	for _, codec := range []ocf.CodecName{ocf.Null, ocf.Deflate, ocf.Snappy, ocf.ZStandard} {
		t.Run(string(codec), func(t *testing.T) {
			var buf bytes.Buffer
			enc, err := ocf.NewEncoder(`"string"`, &buf, ocf.WithCodec(codec), ocf.WithBlockLength(1))
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			for _, record := range []string{"small", big} {
				if err := enc.Encode(record); err != nil {
					t.Fatalf("Encode: %v", err)
				}
			}
			if err := enc.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			res, err := Convert(ConvertRequest{Data: buf.Bytes()})
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if len(res.Script.Actions) != 1 || res.Script.Actions[0].Body != "small" {
				t.Fatalf("actions = %+v", res.Script.Actions)
			}
			if !hasWarningContaining(res.Warnings, "decompresses beyond the 64 KiB import limit") {
				t.Fatalf("missing decompression-limit warning: %v", res.Warnings)
			}
		})
	}
}

// Blocks past max_events are counted from their headers and never
// decompressed, so neither their size nor their decode cost matters.
func TestConvertCountsBlocksPastTheCapWithoutDecodingThem(t *testing.T) {
	withMaxDecodedBytes(t, 64<<10)
	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(`"string"`, &buf, ocf.WithCodec(ocf.Deflate), ocf.WithBlockLength(1))
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, record := range []string{"kept", strings.Repeat("b", 200<<10), "counted"} {
		if err := enc.Encode(record); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	res, err := Convert(ConvertRequest{Data: buf.Bytes(), MaxEvents: 1})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Script.Actions) != 1 || res.Stats.RecordsRead != 3 {
		t.Fatalf("actions = %d stats = %+v", len(res.Script.Actions), res.Stats)
	}
	if hasWarningContaining(res.Warnings, "decompresses beyond") {
		t.Fatalf("a block past the cap was decompressed: %v", res.Warnings)
	}
	if !hasWarningContaining(res.Warnings, "file contained 3 records; seeded the first 1") {
		t.Fatalf("missing cap warning: %v", res.Warnings)
	}
}

// An array's declared block count is allocated before any element is read, and
// null elements need no bytes at all.
func TestConvertBoundsArrayAllocation(t *testing.T) {
	const schema = `{"type":"record","name":"Batch","fields":[{"name":"items","type":{"type":"array","items":"null"}}]}`
	header := ocfHeader(t, schema)
	record := append(zigzag(1<<40), zigzag(0)...)
	data := appendBlock(append([]byte(nil), header...), header, 1, record)

	started := time.Now()
	_, err := Convert(ConvertRequest{Data: data})
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Convert took %s; the declared array count was trusted", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "MaxSliceAllocSize") {
		t.Fatalf("err = %v, want the array allocation bound", err)
	}
}

func TestConvertClampsMaxEvents(t *testing.T) {
	records := make([]any, maxMaxEvents+5)
	for i := range records {
		records[i] = "event"
	}
	res, err := Convert(ConvertRequest{Data: writeOCF(t, `"string"`, records...), MaxEvents: 1 << 30})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Script.Actions) != maxMaxEvents || res.Stats.RecordsRead != maxMaxEvents+5 {
		t.Fatalf("actions = %d stats = %+v", len(res.Script.Actions), res.Stats)
	}
	if !hasWarningContaining(res.Warnings, "exceeds the import limit of 1000") {
		t.Fatalf("missing clamp warning: %v", res.Warnings)
	}
}

// When nothing decodes, the error must carry the decoder's reason rather than
// claim the file was empty.
func TestConvertReportsWhyNoRecordDecoded(t *testing.T) {
	header := ocfHeader(t, `"string"`)
	corrupt := appendBlock(append([]byte(nil), header...), header, 1, zigzag(50)) // string length 50, no bytes
	if _, err := Convert(ConvertRequest{Data: corrupt}); err == nil || !strings.HasPrefix(err.Error(), "Avro file contained no records: ") {
		t.Fatalf("err = %v, want the decode failure", err)
	}

	var buf bytes.Buffer
	enc, err := ocf.NewEncoder(captureSchema, &buf, ocf.WithCodec(ocf.Snappy))
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.Encode(captureRecord(1, "k", "s", "payload")); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	capture := buf.Bytes()
	capture[len(capture)-ocfSyncSize-1] ^= 0xff // break the block's snappy CRC
	if _, err := Convert(ConvertRequest{Data: capture}); err == nil || !strings.Contains(err.Error(), "capture file contained no events: ") || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want the snappy checksum failure", err)
	}
}
