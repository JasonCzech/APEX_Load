package java_vuser

import (
	"strings"
	"testing"
)

func TestCappedBufferTruncatesOverflow(t *testing.T) {
	c := &cappedBuffer{max: 16}
	n, err := c.Write([]byte(strings.Repeat("a", 100)))
	if err != nil || n != 100 {
		t.Fatalf("Write returned (%d,%v); want (100,nil) so the runner is not SIGPIPE'd", n, err)
	}
	if c.buf.Len() != 16 {
		t.Fatalf("captured %d bytes, want cap 16", c.buf.Len())
	}
	if !c.truncated {
		t.Fatal("truncated flag not set after overflow")
	}
	// A subsequent write past the cap is dropped but still reported as written.
	n, _ = c.Write([]byte("more"))
	if n != 4 || c.buf.Len() != 16 {
		t.Fatalf("post-cap write: n=%d len=%d, want n=4 len=16", n, c.buf.Len())
	}
}

func TestCappedBufferUnderCapKeepsAll(t *testing.T) {
	c := &cappedBuffer{max: 64}
	c.Write([]byte("hello"))
	if c.buf.String() != "hello" || c.truncated {
		t.Fatalf("under-cap write changed content/flag: %q truncated=%v", c.buf.String(), c.truncated)
	}
}
