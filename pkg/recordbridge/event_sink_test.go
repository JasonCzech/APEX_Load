package recordbridge

import "encoding/json"

type testEvent struct {
	payload           []byte
	projectID, testID string
	broadcastAll      bool
}
type testEventSink struct{ broadcast chan testEvent }

func newTestEventSink() *testEventSink { return &testEventSink{broadcast: make(chan testEvent, 10000)} }
func (sink *testEventSink) BroadcastScopedJSON(testID, projectID string, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sink.broadcast <- testEvent{payload: payload, projectID: projectID, testID: testID}
}

func boolPtr(value bool) *bool { return &value }
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
