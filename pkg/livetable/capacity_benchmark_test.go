package livetable

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func capacityBenchmarkTable(b *testing.B, profile Profile, receipts int) *Table {
	b.Helper()
	rows := make([]Row, 10000)
	for i := range rows {
		rows[i] = Row{Values: map[string]string{"value": strings.Repeat("x", 400)}}
	}
	table, err := New(Config{ID: "capacity", RunID: "bounded-local-probe", Profile: profile, Columns: []string{"value"}, Rows: rows,
		Limits: Limits{MaxBytes: 8 << 20, MaxRows: 10000, MaxResultBytes: 256 << 10, MaxReceiptBytes: 2 << 20, MaxSnapshotBytes: 14 << 20}})
	if err != nil {
		b.Fatal(err)
	}
	for i := range receipts {
		request := Request{ClientID: "retained", Sequence: uint64(i + 1), Operation: OpUpdate, Values: map[string]string{"value": "unused"}, Expected: map[string]string{"value": "not present"}}
		if profile == ProfileNative {
			request.RecordID = "seed:1"
		} else {
			request.Position = 1
		}
		if _, err := table.Execute(context.Background(), request); err != nil {
			b.Fatal(err)
		}
	}
	return table
}

// Run with -benchtime=100x. The retained set is deliberately real and does not
// expire while measured. This is a bounded local core/JSON probe, not a storage
// fsync, distributed throughput, or production capacity claim.
func BenchmarkLiveTableRetainedReceipts(b *testing.B) {
	for _, profile := range []Profile{ProfileNative, ProfileVTS} {
		for _, receipts := range []int{0, 1000, 3000} {
			b.Run(fmt.Sprintf("%s/receipts=%d", profile, receipts), func(b *testing.B) {
				if b.N > 1000 {
					b.Skip("bounded acceptance probe: use -benchtime=100x")
				}
				table := capacityBenchmarkTable(b, profile, receipts)
				request := Request{ClientID: "measured", Operation: OpUpdate, Values: map[string]string{"value": strings.Repeat("y", 400)}}
				if profile == ProfileNative {
					request.RecordID = "seed:1"
				} else {
					request.Operation, request.Values = OpRotate, nil
				}
				b.ReportAllocs()
				b.ResetTimer()
				var slowest time.Duration
				for i := range b.N {
					request.Sequence = uint64(i + 1)
					start := time.Now()
					if _, err := table.Execute(context.Background(), request); err != nil {
						b.Fatal(err)
					}
					slowest = max(slowest, time.Since(start))
				}
				b.StopTimer()
				b.ReportMetric(float64(slowest.Nanoseconds())/1e6, "max_ms/op")
				b.ReportMetric(float64(table.Stats().Receipts), "retained_receipts")
			})
		}
	}
}

func BenchmarkLiveTableBoundedSnapshot(b *testing.B) {
	for _, profile := range []Profile{ProfileNative, ProfileVTS} {
		b.Run(string(profile), func(b *testing.B) {
			table := capacityBenchmarkTable(b, profile, 3000)
			b.ReportAllocs()
			b.ResetTimer()
			var bytes int
			var slowest time.Duration
			for range b.N {
				start := time.Now()
				snapshot, err := table.Snapshot()
				if err != nil {
					b.Fatal(err)
				}
				slowest = max(slowest, time.Since(start))
				bytes = len(snapshot.Payload)
			}
			b.StopTimer()
			b.ReportMetric(float64(slowest.Nanoseconds())/1e6, "max_ms/snapshot")
			b.ReportMetric(float64(bytes), "snapshot_B")
		})
	}
}

func BenchmarkLiveTableRenewalWithActiveLeases(b *testing.B) {
	if b.N > 1000 {
		b.Skip("bounded acceptance probe: use -benchtime=100x")
	}
	table := capacityBenchmarkTable(b, ProfileNative, 0)
	var first Result
	for i := range 256 {
		result, err := table.Execute(context.Background(), Request{ClientID: "worker", Sequence: uint64(i + 1), Operation: OpLease})
		if err != nil {
			b.Fatal(err)
		}
		if i == 0 {
			first = result
		}
	}
	request := Request{ClientID: "renew", OwnerID: "worker", Operation: OpRenew, RecordID: first.Row.ID, LeaseToken: first.Lease.Token}
	b.ReportAllocs()
	b.ResetTimer()
	var slowest time.Duration
	for i := range b.N {
		request.Sequence = uint64(i + 1)
		start := time.Now()
		if _, err := table.Execute(context.Background(), request); err != nil {
			b.Fatal(err)
		}
		slowest = max(slowest, time.Since(start))
	}
	b.StopTimer()
	b.ReportMetric(float64(slowest.Nanoseconds())/1e6, "max_ms/renew")
}

func BenchmarkLiveTableStopWithWaiters(b *testing.B) {
	for _, profile := range []Profile{ProfileNative, ProfileVTS} {
		b.Run(string(profile), func(b *testing.B) {
			b.StopTimer()
			var slowest time.Duration
			for range b.N {
				table, err := New(Config{ID: "stop", RunID: "bounded-local-probe", Profile: profile, Columns: []string{"value"}, Limits: Limits{MaxWaiters: 64, MaxResultBytes: 1024, MaxReceiptBytes: 1 << 20}})
				if err != nil {
					b.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				done := make(chan error, 64)
				for i := range 64 {
					go func(i int) {
						_, err := table.Execute(ctx, Request{ClientID: fmt.Sprint(i), Sequence: 1, Operation: OpPop, Wait: time.Minute})
						done <- err
					}(i)
				}
				for table.Stats().Waiters != 64 && ctx.Err() == nil {
					time.Sleep(time.Millisecond)
				}
				if ctx.Err() != nil {
					cancel()
					b.Fatal("waiters did not become ready")
				}
				b.StartTimer()
				start := time.Now()
				err = table.Close(ctx)
				for range 64 {
					if result := <-done; CodeOf(result) != CodeClosed {
						b.Errorf("waiter did not close: %v", result)
					}
				}
				slowest = max(slowest, time.Since(start))
				b.StopTimer()
				cancel()
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(slowest.Nanoseconds())/1e6, "max_ms/stop")
		})
	}
}
