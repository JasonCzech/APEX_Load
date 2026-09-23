package http

import "golang.org/x/text/encoding/charmap"

func encodeWindows1252Body(body []byte) ([]byte, error) {
	return charmap.Windows1252.NewEncoder().Bytes(body)
}
