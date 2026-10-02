package eventhubs

import "testing"

func TestProcessorSettingsPreserveObserverAndRejectUnsupportedModes(t *testing.T) {
	if err := ValidateProcessorSettings("", "", "", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	valid := func(mode, storage, auth, ns, group, partition string, mock bool) bool {
		return ValidateProcessorSettings(mode, storage, auth, ns, group, partition, mock) == nil
	}
	if !valid("processor", "https://account.blob.core.windows.net/checkpoints", "aad", "namespace.servicebus.windows.net", "workers", "", false) {
		t.Fatal("valid processor rejected")
	}
	for _, tc := range []struct {
		mode, storage, auth, ns, group, partition string
		mock                                      bool
	}{{"other", "", "", "", "", "", false}, {"observer", "https://store/checkpoints", "", "", "", "", false}, {"processor", "http://store/checkpoints", "aad", "ns", "g", "", false}, {"processor", "https://store/checkpoints", "connection_string", "ns", "g", "", false}, {"processor", "https://store/checkpoints", "aad", "ns", "", "", false}, {"processor", "https://store/checkpoints", "aad", "ns", "g", "0", false}, {"processor", "https://store/checkpoints", "aad", "ns", "g", "", true}} {
		if valid(tc.mode, tc.storage, tc.auth, tc.ns, tc.group, tc.partition, tc.mock) {
			t.Fatalf("unsupported configuration accepted %+v", tc)
		}
	}
}
