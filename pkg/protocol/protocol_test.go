package protocol

import "testing"

func TestClassifyPayloadSize(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{name: "empty", bytes: 0, expected: ""},
		{name: "small", bytes: 8 * 1024, expected: "small"},
		{name: "medium lower", bytes: 16 * 1024, expected: "medium"},
		{name: "medium upper", bytes: 64 * 1024, expected: "medium"},
		{name: "large", bytes: 128 * 1024, expected: "large"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ClassifyPayloadSize(testCase.bytes); got != testCase.expected {
				t.Fatalf("ClassifyPayloadSize(%d) = %q, want %q", testCase.bytes, got, testCase.expected)
			}
		})
	}
}