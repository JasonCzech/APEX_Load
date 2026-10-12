package livetable

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Concurrent groups share a real table and retained receipts. Every operation
// is retried while unrelated work continues, then the committed journal is
// replayed and compared with the same results and final data.
func TestConcurrentProducersConsumersReconcileThroughRecovery(t *testing.T) {
	for _, profile := range []Profile{ProfileNative, ProfileVTS} {
		t.Run(string(profile), func(t *testing.T) {
			const workers, perWorker = 4, 24
			table := tableFor(t, profile, nil, Limits{MaxResultBytes: 4096, MaxReceiptBytes: 2 << 20})
			seed, err := table.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			var journal []Entry
			table.committer = CommitFunc(func(_ context.Context, entry Entry) error {
				journal = append(journal, entry) // Commit is serialized by the table.
				return nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := make(chan struct{})
			type observation struct {
				request Request
				result  Result
			}
			results := make(chan observation, workers*perWorker*2)
			errors := make(chan error, workers*2)
			var wg sync.WaitGroup
			for worker := range workers {
				for _, producer := range []bool{false, true} {
					wg.Add(1)
					go func(worker int, producer bool) {
						defer wg.Done()
						<-start
						for i := range perWorker {
							request := Request{ClientID: fmt.Sprintf("worker-%d-%v", worker, producer), Sequence: uint64(i + 1), Operation: OpPop, Wait: time.Second}
							if producer {
								value := fmt.Sprintf("%d/%d", worker, i)
								request.Operation, request.Wait = OpPush, 0
								request.Values = map[string]string{"name": value, "n": "paired:" + value}
							}
							first, err := table.Execute(ctx, request)
							if err != nil {
								errors <- err
								return
							}
							again, err := table.Execute(ctx, request)
							if err != nil || !reflect.DeepEqual(first, again) {
								errors <- fmt.Errorf("retry changed concurrent outcome: %v", err)
								return
							}
							results <- observation{request, first}
						}
					}(worker, producer)
				}
			}
			close(start)
			wg.Wait()
			close(errors)
			close(results)
			for err := range errors {
				t.Fatal(err)
			}
			restored, err := Restore(seed, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range journal {
				if err := restored.Replay(entry); err != nil {
					t.Fatal(err)
				}
			}
			seen := map[string]bool{}
			for observed := range results {
				if replay := execute(t, restored, observed.request); !reflect.DeepEqual(observed.result, replay) {
					t.Fatal("recovery changed a retained concurrent outcome")
				}
				if observed.request.Operation != OpPop {
					continue
				}
				if observed.result.Status != StatusOK || observed.result.Row == nil {
					t.Fatal("consumer lost a produced value")
				}
				value := observed.result.Row.Values["name"]
				if seen[value] || observed.result.Row.Values["n"] != "paired:"+value {
					t.Fatal("duplicate consumption or non-atomic paired-column mutation")
				}
				seen[value] = true
			}
			livePage, liveErr := table.Inspect(0, 1, 0)
			recoveredPage, recoveredErr := restored.Inspect(0, 1, 0)
			if liveErr != nil || recoveredErr != nil || !reflect.DeepEqual(livePage, recoveredPage) {
				t.Fatalf("recovered final data differs: live=%+v/%v recovered=%+v/%v", livePage, liveErr, recoveredPage, recoveredErr)
			}
			if len(seen) != workers*perWorker || table.s.Revision != restored.s.Revision || table.s.Counts != restored.s.Counts || restored.Stats().Counts.Available != 0 {
				t.Fatalf("concurrent run did not reconcile: consumed=%d stats=%+v", len(seen), restored.Stats())
			}
		})
	}
}

func TestStopRemainsResponsiveWithSaturatedWaiters(t *testing.T) {
	for _, profile := range []Profile{ProfileNative, ProfileVTS} {
		t.Run(string(profile), func(t *testing.T) {
			const waiters = 64
			table := tableFor(t, profile, nil, Limits{MaxWaiters: waiters, MaxResultBytes: 1024, MaxReceiptBytes: 1 << 20})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, waiters)
			for i := range waiters {
				go func(i int) {
					_, err := table.Execute(ctx, Request{ClientID: fmt.Sprintf("waiter-%d", i), Sequence: 1, Operation: OpPop, Wait: time.Minute})
					done <- err
				}(i)
			}
			waitFor(t, func() bool { return table.Stats().Waiters == waiters })
			if _, err := table.Execute(ctx, Request{ClientID: "overflow", Sequence: 1, Operation: OpPop, Wait: time.Minute}); CodeOf(err) != CodeCapacity {
				t.Fatalf("waiter overflow was not definite: %v", err)
			}
			closed := make(chan error, 1)
			go func() { closed <- table.Close(ctx) }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("stop was blocked behind waiting data operations")
			}
			for range waiters {
				select {
				case err := <-done:
					if CodeOf(err) != CodeClosed {
						t.Fatalf("waiter was not closed: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("closed waiter leaked")
				}
			}
			if table.Stats().Waiters != 0 {
				t.Fatal("closed table retained waiters")
			}
		})
	}
}
