package main

import (
	"errors"
	"testing"
)

func TestBaselineVerdictCIGate(t *testing.T) {
	for _, tc := range []struct {
		body string
		code int
	}{{`{"verdict":{"status":"passed","unavailable":0,"evaluated":3}}`, 0}, {`{"verdict":{"status":"regressed","unavailable":1}}`, exitCodeTestFailure}, {`{"verdict":{"status":"not_evaluated"}}`, exitCodeControllerOrTransport}, {`{"verdict":{"status":"passed","unavailable":1}}`, exitCodeControllerOrTransport}, {`{"verdict":{"status":"passed","unavailable":0,"evaluated":0}}`, exitCodeControllerOrTransport}, {`{}`, exitCodeControllerOrTransport}, {`broken`, exitCodeControllerOrTransport}} {
		err := checkBaselineVerdict([]byte(tc.body))
		if tc.code == 0 {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var e *baselineVerdictError
		if !errors.As(err, &e) || e.code != tc.code {
			t.Fatalf("%s: %v", tc.body, err)
		}
	}
}
