package http

import (
	"fmt"
	"io"
)

type responseReader struct{ io.Reader }
type responseWriter struct{ io.Writer }

func copyResponse(dst io.Writer, src io.Reader, size int) (int64, error) {
	if size == 0 {
		return io.Copy(dst, src)
	}
	lease := processHTTPResponseCaptureBudget.lease()
	defer lease.release()
	if !lease.reserve(int64(size)) {
		return 0, fmt.Errorf("process HTTP receive-buffer budget exhausted")
	}
	return io.CopyBuffer(responseWriter{dst}, responseReader{src}, make([]byte, size))
}
