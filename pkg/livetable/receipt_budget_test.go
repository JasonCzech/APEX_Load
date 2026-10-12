package livetable

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLeaseSettlementSurvivesOrdinaryReceiptSaturation(t *testing.T) {
	for _, settlement := range []Operation{OpAck, OpRelease} {
		for _, restore := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/restored=%v", settlement, restore), func(t *testing.T) {
				table := tableFor(t, ProfileNative, []Row{row("work", "1")}, Limits{MaxReceipts: 32, MaxResultBytes: 1024, MaxReceiptBytes: 64 << 10})
				grant := execute(t, table, Request{ClientID: "worker", Sequence: 1, Operation: OpLease})
				for seq := uint64(1); ; seq++ {
					_, err := table.Execute(context.Background(), Request{ClientID: "producer", Sequence: seq, Operation: OpPush, Values: map[string]string{"name": fmt.Sprint(seq)}})
					if CodeOf(err) == CodeCapacity {
						break
					}
					if err != nil || seq >= 32 {
						t.Fatalf("ordinary saturation: seq=%d err=%v", seq, err)
					}
				}
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
				// Renewal has its own lane, as in the engine. It cannot spend the
				// settlement slot belonging to this acquisition.
				for seq := uint64(1); ; seq++ {
					_, err := table.Execute(context.Background(), Request{ClientID: "renew", OwnerID: "worker", Sequence: seq, Operation: OpRenew, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token})
					if CodeOf(err) == CodeCapacity {
						if seq == 1 {
							t.Fatal("ordinary saturation consumed every renewal slot")
						}
						break
					}
					if err != nil || seq >= 32 {
						t.Fatalf("renewal saturation: seq=%d err=%v", seq, err)
					}
				}
				request := Request{ClientID: "worker", Sequence: 2, Operation: settlement, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token}
				result := execute(t, table, request)
				if replay := execute(t, table, request); !reflect.DeepEqual(result, replay) {
					t.Fatal("settlement retry changed its outcome")
				}
				stats := table.Stats()
				if stats.Counts.Leased != 0 || stats.Receipts > 32 || stats.ReceiptBytes > 64<<10 {
					t.Fatalf("settlement leaked or exceeded total limits: %+v", stats)
				}
			})
		}
	}
}

func TestLeaseSettlementReservationProtectsBytesAndLowLimits(t *testing.T) {
	t.Run("one receipt cannot admit a lease", func(t *testing.T) {
		table := tableFor(t, ProfileNative, []Row{row("work", "1")}, Limits{MaxReceipts: 1})
		if _, err := table.Execute(context.Background(), Request{ClientID: "worker", Sequence: 1, Operation: OpLease}); CodeOf(err) != CodeCapacity {
			t.Fatalf("lease without a settlement slot: %v", err)
		}
		if table.Stats().Counts.Leased != 0 || table.Stats().Revision != 0 {
			t.Fatal("rejected lease changed state")
		}
	})
	t.Run("two receipts settle exactly once", func(t *testing.T) {
		table := tableFor(t, ProfileNative, []Row{row("work", "1")}, Limits{MaxReceipts: 2})
		grant := execute(t, table, Request{ClientID: "worker", Sequence: 1, Operation: OpLease})
		execute(t, table, Request{ClientID: "worker", Sequence: 2, Operation: OpAck, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token})
		if table.Stats().Receipts != 2 || table.Stats().Counts.Acknowledged != 1 {
			t.Fatal("settlement did not use the reserved receipt")
		}
	})
	t.Run("byte pressure", func(t *testing.T) {
		table := tableFor(t, ProfileNative, []Row{row(strings.Repeat("x", 6000), "1")}, Limits{MaxReceipts: 1000, MaxResultBytes: 8192, MaxReceiptBytes: 64 << 10})
		grant := execute(t, table, Request{ClientID: "worker", Sequence: 1, Operation: OpLease})
		for seq := uint64(1); ; seq++ {
			_, err := table.Execute(context.Background(), Request{ClientID: "producer", Sequence: seq, Operation: OpPush, Values: map[string]string{"name": strings.Repeat("y", 6000)}})
			if CodeOf(err) == CodeCapacity {
				break
			}
			if err != nil || seq > 100 {
				t.Fatalf("byte saturation: seq=%d err=%v", seq, err)
			}
		}
		request := Request{ClientID: strings.Repeat("w", 256), OwnerID: "worker", Sequence: 1, Operation: OpAck, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token}
		execute(t, table, request)
		if stats := table.Stats(); stats.Counts.Acknowledged != 1 || stats.ReceiptBytes > 64<<10 {
			t.Fatalf("byte reservation failed: %+v", stats)
		}
	})
}

func TestPendingLeaseReservationSettlesWithoutDoubleCounting(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{MaxReceipts: 3, MaxResultBytes: 1024, MaxReceiptBytes: 16 << 10})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := Request{ClientID: "worker", Sequence: 1, Operation: OpLease, Wait: time.Second}
	done := make(chan Result, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() { result, err := table.Execute(ctx, request); done <- result; errs <- err }()
	}
	waitFor(t, func() bool { return table.Stats().Waiters == 1 })
	execute(t, table, Request{ClientID: "producer", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "work"}})
	first, second := <-done, <-done
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.Lease == nil {
		t.Fatal("duplicate pending lease acquired twice")
	}
	execute(t, table, Request{ClientID: "worker", Sequence: 2, Operation: OpAck, RecordID: first.Row.ID, LeaseToken: first.Lease.Token})
	if stats := table.Stats(); stats.Receipts != 3 || stats.Counts.Leased != 0 || stats.Waiters != 0 {
		t.Fatalf("pending reservation leaked: %+v", stats)
	}
}

func TestCanceledAndExpiredLeasesReleaseSettlementReservations(t *testing.T) {
	t.Run("canceled waiter", func(t *testing.T) {
		table := tableFor(t, ProfileNative, nil, Limits{MaxReceipts: 2})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := table.Execute(ctx, Request{ClientID: "worker", Sequence: 1, Operation: OpLease, Wait: time.Minute})
			done <- err
		}()
		waitFor(t, func() bool { return table.Stats().Waiters == 1 })
		cancel()
		if err := <-done; CodeOf(err) != CodeCanceled {
			t.Fatal(err)
		}
		execute(t, table, Request{ClientID: "producer", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "new"}})
	})
	t.Run("expired acquisition", func(t *testing.T) {
		now := time.Now()
		table := tableFor(t, ProfileNative, []Row{row("work", "1")}, Limits{MaxReceipts: 2, DefaultLeaseDuration: time.Second})
		table.now = func() time.Time { return now }
		execute(t, table, Request{ClientID: "worker", Sequence: 1, Operation: OpLease})
		now = now.Add(2 * time.Second)
		execute(t, table, Request{ClientID: "producer", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "new"}})
		if stats := table.Stats(); stats.Counts.Leased != 0 || stats.Receipts != 2 {
			t.Fatalf("expired reservation leaked: %+v", stats)
		}
	})
}

func TestLeaseReservationsProtectCheckpointCapacity(t *testing.T) {
	t.Run("pending lease is rejected before accepting unpersistable cancellation", func(t *testing.T) {
		table := tableFor(t, ProfileNative, nil, Limits{MaxSnapshotBytes: 24 << 10, MaxResultBytes: 1024})
		_, err := table.Execute(context.Background(), Request{ClientID: "worker", Sequence: 1, Operation: OpLease, Wait: time.Minute})
		if CodeOf(err) != CodeCapacity || table.Stats().Waiters != 0 || table.Stats().Revision != 0 {
			t.Fatalf("uncheckpointable wait was admitted: %v %+v", err, table.Stats())
		}
	})
	t.Run("settlement client can use reserved recovery metadata", func(t *testing.T) {
		table := tableFor(t, ProfileNative, []Row{row("work", "1")}, Limits{MaxSnapshotBytes: 96 << 10, MaxResultBytes: 1024})
		grant := execute(t, table, Request{ClientID: "worker", Sequence: 1, Operation: OpLease})
		for seq := uint64(1); ; seq++ {
			_, err := table.Execute(context.Background(), Request{ClientID: "producer", Sequence: seq, Operation: OpPop})
			if CodeOf(err) == CodeCapacity {
				break
			}
			if err != nil || seq > 100 {
				t.Fatalf("checkpoint pressure: %v", err)
			}
		}
		execute(t, table, Request{ClientID: "settlement", OwnerID: "worker", Sequence: 1, Operation: OpAck, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token})
		if _, err := table.Snapshot(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLeaseRenewalAndSettlementSurviveCheckpointSaturation(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprint(restore), func(t *testing.T) {
			table := tableFor(t, ProfileNative, []Row{row("work", "1")}, Limits{MaxSnapshotBytes: 128 << 10, MaxResultBytes: 1024})
			grant := execute(t, table, Request{ClientID: "worker", Sequence: 1, Operation: OpLease})
			for seq := uint64(1); ; seq++ {
				_, err := table.Execute(t.Context(), Request{ClientID: "producer", Sequence: seq, Operation: OpPop})
				if CodeOf(err) == CodeCapacity {
					if !strings.Contains(err.Error(), "checkpoint") {
						t.Fatalf("fixture hit a different budget: %v", err)
					}
					break
				}
				if err != nil || seq > 200 {
					t.Fatalf("checkpoint pressure: %v", err)
				}
			}
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
			execute(t, table, Request{ClientID: "renewal", OwnerID: "worker", Sequence: 1, Operation: OpRenew, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token})
			execute(t, table, Request{ClientID: "settlement", OwnerID: "worker", Sequence: 1, Operation: OpAck, RecordID: grant.Row.ID, LeaseToken: grant.Lease.Token})
			if _, err := table.Snapshot(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPendingLeaseCheckpointReservationSurvivesCancellationAtCapacity(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{MaxSnapshotBytes: 512 << 10, MaxResultBytes: 1024, MaxReceipts: 128})
	type waiter struct {
		request Request
		cancel  context.CancelFunc
		done    chan error
	}
	var waiting []waiter
	for i := 0; i < 64; i++ {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		r := Request{ClientID: fmt.Sprintf("worker-%d", i), Sequence: 1, Operation: OpLease, Wait: time.Minute}
		done := make(chan error, 1)
		go func() { _, err := table.Execute(ctx, r); done <- err }()
		waitFor(t, func() bool { return table.Stats().Waiters == len(waiting)+1 || len(done) > 0 })
		select {
		case err := <-done:
			if CodeOf(err) != CodeCapacity {
				t.Fatalf("admission: %v", err)
			}
			cancel()
			if len(waiting) < 2 {
				t.Fatal("fixture admitted too few waits")
			}
			goto cancelWaits
		default:
			waiting = append(waiting, waiter{r, cancel, done})
		}
	}
	t.Fatal("fixture never reached checkpoint capacity")
cancelWaits:
	for _, w := range waiting {
		w.cancel()
		if err := <-w.done; CodeOf(err) != CodeCanceled {
			t.Fatalf("accepted wait could not persist cancellation: %v", err)
		}
		if _, err := table.Execute(t.Context(), w.request); CodeOf(err) != CodeCanceled {
			t.Fatalf("cancellation retry changed: %v", err)
		}
	}
	if _, err := table.Snapshot(); err != nil {
		t.Fatal(err)
	}
}
