package livetable

import (
	"context"
	"fmt"
	"testing"
)

func TestLeaseReservesRenewalAndSettlementClientIdentities(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprint(restore), func(t *testing.T) {
			table := tableFor(t, ProfileNative, []Row{row("work", "1")}, Limits{MaxClients: 6})
			grant := execute(t, table, Request{ClientID: "main", OwnerID: "worker", Sequence: 1, Operation: OpLease})
			for i := 0; i < 3; i++ {
				execute(t, table, Request{ClientID: fmt.Sprint("producer", i), Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "next"}})
			}
			if _, err := table.Execute(context.Background(), Request{ClientID: "overflow", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "next"}}); CodeOf(err) != CodeCapacity {
				t.Fatalf("producer consumed reserved clients: %v", err)
			}
			execute(t, table, Request{ClientID: "renew", OwnerID: "worker", Sequence: 1, Operation: OpRenew, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token})
			if restore {
				snapshot, err := table.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				table, err = Restore(snapshot, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			execute(t, table, Request{ClientID: "settle", OwnerID: "worker", Sequence: 1, Operation: OpAck, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token})
			if table.Stats().Counts.Leased != 0 {
				t.Fatal("lease not settled")
			}
		})
	}
}
func TestLeaseCannotAcquireWithoutControlClientBudget(t *testing.T) {
	table := tableFor(t, ProfileNative, []Row{row("work", "1")}, Limits{MaxClients: 2})
	if _, err := table.Execute(context.Background(), Request{ClientID: "worker", Sequence: 1, Operation: OpLease}); CodeOf(err) != CodeCapacity {
		t.Fatalf("acquisition without control identities: %v", err)
	}
	if table.Stats().Revision != 0 {
		t.Fatal("rejected acquisition mutated table")
	}
}
