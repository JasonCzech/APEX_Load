package browser

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestHAROwnershipCompletedDetachErrorRequiresExactWriterJoin(t *testing.T) {
	for _, fails := range []bool{false, true} {
		name := "joined-writer"
		if fails {
			name = "unproven-writer"
		}
		t.Run(name, func(t *testing.T) {
			owner := fixtureHAROwner(t, t.TempDir())
			writeOwnedHARFixture(t, owner)
			if err := owner.beginWriter(); err != nil {
				t.Fatal(err)
			}
			closeEntered := make(chan struct{})
			closeFailure := errors.New("synthetic owned HAR close failed")
			owner.bindWriter(func() error {
				close(closeEntered)
				if fails {
					return closeFailure
				}
				return nil
			})
			b := &Browser{harOwner: owner, harWorkingPath: owner.path, harCapturePolicy: HARCaptureAlways,
				pwCDP: &harDetachFixture{detach: func() error {
					<-closeEntered
					return errors.New("playwright: Protocol error (Target.detachFromTarget): No session with given id")
				}},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := b.Teardown(ctx)
			if fails {
				if !errors.Is(err, closeFailure) || !errors.Is(err, errHAROwnershipUnproven) || owner.retiredSuccessfully() || len(b.publishedHARArtifacts) != 0 {
					t.Fatalf("failed writer close was treated as joined: %v", err)
				}
				if _, statErr := os.Stat(owner.path); statErr != nil {
					t.Fatal("unproven raw writer was removed", statErr)
				}
				return
			}
			if err != nil || !owner.retiredSuccessfully() || len(b.publishedHARArtifacts) != 1 {
				t.Fatalf("completed CDP detach error hid a proven HAR: %v", err)
			}
			artifact := b.publishedHARArtifacts[0]
			if artifact.Generation != owner.generation {
				t.Fatal("published a different owner")
			}
			if _, err := os.Stat(artifact.Path); err != nil {
				t.Fatal("joined HAR was not published", err)
			}
			if _, err := os.Stat(owner.path); !os.IsNotExist(err) {
				t.Fatal("joined raw generation was not retired", err)
			}
		})
	}
}
