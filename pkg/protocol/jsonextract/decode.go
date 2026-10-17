package jsonextract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// DecodeExact decodes one complete JSON value without rounding numeric tokens.
// JSONPath and payload projections use this; JMESPath keeps its float64 model
// because its numeric operators require that representation.
func DecodeExact(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("unexpected value after JSON document")
	}
	return nil
}
