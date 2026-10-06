package har

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// TruncationSalvage describes a HAR file that ends partway through its entries.
type TruncationSalvage struct {
	Entries      int // complete entries before the cut
	DroppedBytes int // bytes after the last complete entry
}

// Warning explains what a salvaged import keeps and loses.
func (s TruncationSalvage) Warning() string {
	return fmt.Sprintf("The HAR file is truncated: %d complete %s recovered, and the incomplete entry and everything after it (%d bytes) were discarded. The script ends where the capture was cut, so later steps and any values they produce are missing; re-capture the session for a complete script.", s.Entries, pluralEntries(s.Entries), s.DroppedBytes)
}

// Hint explains how to import a truncated HAR that was not salvaged.
func (s TruncationSalvage) Hint() string {
	return fmt.Sprintf("the HAR file is truncated after %d complete %s; re-capture the session, or import it again with \"Recover the complete entries of a truncated HAR\" (salvage_truncated=true) to keep those entries", s.Entries, pluralEntries(s.Entries))
}

func pluralEntries(n int) string {
	if n == 1 {
		return "entry"
	}
	return "entries"
}

// SalvageTruncated rebuilds a HAR file that was cut off partway through
// log.entries, keeping the log fields before the entries and every complete
// entry. It reports false for valid JSON, and for a file that is malformed
// rather than cut off or has no complete entry: only a file that ends early is
// salvaged, so corruption in the middle is never silently discarded.
func SalvageTruncated(data []byte) ([]byte, TruncationSalvage, bool) {
	data = bytes.TrimPrefix(data, []byte("\uFEFF"))
	if json.Valid(data) {
		return nil, TruncationSalvage{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !expectDelim(decoder, '{') {
		return nil, TruncationSalvage{}, false
	}
	for decoder.More() {
		key, ok := nextKey(decoder)
		if !ok {
			return nil, TruncationSalvage{}, false
		}
		if key != "log" {
			var skipped json.RawMessage
			if decoder.Decode(&skipped) != nil {
				return nil, TruncationSalvage{}, false
			}
			continue
		}
		return salvageLog(decoder, data)
	}
	return nil, TruncationSalvage{}, false
}

func salvageLog(decoder *json.Decoder, data []byte) ([]byte, TruncationSalvage, bool) {
	if !expectDelim(decoder, '{') {
		return nil, TruncationSalvage{}, false
	}
	var out bytes.Buffer
	out.WriteString(`{"log":{`)
	for decoder.More() {
		key, ok := nextKey(decoder)
		if !ok {
			return nil, TruncationSalvage{}, false
		}
		name, _ := json.Marshal(key)
		if key != "entries" {
			var value json.RawMessage
			if decoder.Decode(&value) != nil {
				return nil, TruncationSalvage{}, false // cut before the entries
			}
			out.Write(name)
			out.WriteByte(':')
			out.Write(value)
			out.WriteByte(',')
			continue
		}
		if !expectDelim(decoder, '[') {
			return nil, TruncationSalvage{}, false
		}
		out.Write(name)
		out.WriteString(":[")
		salvage := TruncationSalvage{}
		end := decoder.InputOffset()
		finish := func(after int64) ([]byte, TruncationSalvage, bool) {
			if salvage.Entries == 0 {
				return nil, TruncationSalvage{}, false
			}
			salvage.DroppedBytes = len(data) - int(after)
			out.WriteString("]}}")
			return out.Bytes(), salvage, true
		}
		for decoder.More() {
			var entry json.RawMessage
			if err := decoder.Decode(&entry); err != nil {
				if !endedEarly(err) {
					return nil, TruncationSalvage{}, false
				}
				return finish(end)
			}
			if salvage.Entries > 0 {
				out.WriteByte(',')
			}
			out.Write(entry)
			salvage.Entries++
			end = decoder.InputOffset()
		}
		token, err := decoder.Token()
		switch {
		case err != nil && endedEarly(err):
			return finish(end) // the cut came right after an entry
		case err != nil || token != json.Delim(']'):
			return nil, TruncationSalvage{}, false
		}
		// The entries closed, so the cut came later: everything after them must
		// parse until the input ends, or the file is malformed, not truncated.
		closed := decoder.InputOffset()
		if !restEndsEarly(decoder) {
			return nil, TruncationSalvage{}, false
		}
		return finish(closed)
	}
	return nil, TruncationSalvage{}, false
}

// restEndsEarly reports whether the remaining log fields and closing braces
// parse until the input runs out.
func restEndsEarly(decoder *json.Decoder) bool {
	for decoder.More() {
		if _, err := decoder.Token(); err != nil {
			return endedEarly(err)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return endedEarly(err)
		}
	}
	for range 2 { // the log object, then the HAR object
		token, err := decoder.Token()
		if err != nil {
			return endedEarly(err)
		}
		if token != json.Delim('}') {
			return false
		}
	}
	return false // complete, so anything that failed to parse came after it
}

// endedEarly reports a decode error caused by the input ending, as opposed to
// malformed content.
func endedEarly(err error) bool {
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

func expectDelim(decoder *json.Decoder, want json.Delim) bool {
	token, err := decoder.Token()
	return err == nil && token == want
}

func nextKey(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	key, ok := token.(string)
	return key, err == nil && ok
}
