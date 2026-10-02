package identitypolicy

import "testing"

func TestCheckpointContainerIdentityIsExactAndFailClosed(t *testing.T) {
	const approved = "https://account.blob.core.windows.net/checkpoints"
	t.Setenv(EventHubsCheckpointContainersEnv, "")
	if _, err := ValidateEventHubsCheckpointContainer(approved); err == nil {
		t.Fatal("ambient storage identity ungoverned")
	}
	t.Setenv(EventHubsCheckpointContainersEnv, approved)
	if got, err := ValidateEventHubsCheckpointContainer(approved); err != nil || got != approved {
		t.Fatalf("%s %v", got, err)
	}
	for _, value := range []string{"http://account.blob.core.windows.net/checkpoints", approved + "?sig=secret", approved + "/other", "https://user:pass@account.blob.core.windows.net/checkpoints", "https://account.blob.core.windows.net/other", "https://account.blob.core.windows.net:443/checkpoints", "https://127.0.0.1/checkpoints"} {
		if _, err := ValidateEventHubsCheckpointContainer(value); err == nil {
			t.Fatalf("untrusted container accepted: %s", value)
		}
	}
}
