package compute

import (
	"context"
	"errors"
	"testing"
)

func TestDiscoverCancellationInstancesRequiresEveryLaunchIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		discovered   []Instance
		discoveryErr error
		wantErr      bool
		wantCount    int
	}{
		{name: "missing", wantErr: true, wantCount: 1},
		{name: "unavailable", discoveryErr: errors.New("provider unavailable"), wantErr: true, wantCount: 1},
		{name: "other operation", discovered: []Instance{{ProviderID: "other", Tags: map[string]string{"apexload.io/operation-id": "other", "apexload.io/pool-id": "pool", "apexload.io/launch-nonce": "n2"}}}, wantErr: true, wantCount: 1},
		{name: "same nonce twice", discovered: []Instance{{ProviderID: "duplicate", Tags: map[string]string{"apexload.io/operation-id": "op", "apexload.io/pool-id": "pool", "apexload.io/launch-nonce": "n1"}}}, wantErr: true, wantCount: 2},
		{name: "recovered", discovered: []Instance{{ProviderID: "recovered", Tags: map[string]string{"apexload.io/operation-id": "op", "apexload.io/pool-id": "pool", "apexload.io/launch-nonce": "n2"}}}, wantCount: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := CapacityRequest{OperationID: "op", FleetPoolID: "pool", ProviderOperationIDs: []string{"known"}, LaunchIntents: []LaunchIntent{{LaunchNonce: "n1"}, {LaunchNonce: "n2"}}}
			instances, err := DiscoverCancellationInstances(context.Background(), request, []Instance{{ProviderID: "known"}}, func(_ context.Context, r CapacityRequest) ([]Instance, error) {
				if len(r.ProviderOperationIDs) != 0 || r.OperationID != "op" || r.FleetPoolID != "pool" {
					t.Fatalf("discovery scope: %+v", r)
				}
				return tc.discovered, tc.discoveryErr
			})
			if (err != nil) != tc.wantErr || len(instances) != tc.wantCount {
				t.Fatalf("instances=%+v, error=%v", instances, err)
			}
		})
	}
}
