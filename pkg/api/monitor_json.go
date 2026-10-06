package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func decodeMonitorJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(value)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = errors.New("trailing JSON")
		}
	}
	if err == nil {
		return true
	}
	var bound *http.MaxBytesError
	if errors.As(err, &bound) {
		writeError(w, 413, "Monitoring request exceeds the configured size limit")
	} else {
		writeError(w, 400, "invalid Monitoring request")
	}
	return false
}
