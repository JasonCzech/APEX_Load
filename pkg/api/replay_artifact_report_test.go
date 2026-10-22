package api

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestReplayArtifactReportRead(t *testing.T) {
	for _, object := range []bool{false, true} {
		t.Run(map[bool]string{false: "filesystem", true: "object"}[object], func(t *testing.T) {
			b, spec, har, report := replayBackendFixture(t, object)
			ctx := context.Background()
			if err := b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
				t.Fatal(err)
			}
			receipt := storage.ReplayArtifactReceipt{Spec: spec, State: "ready", WriterJoined: true}
			got, err := b.ReadReport(ctx, receipt)
			if err != nil || !bytes.Equal(got, report) {
				t.Fatalf("report %q: %v", got, err)
			}
			// Status can report retained metadata without claiming HAR availability.
			if object {
				err = b.bucket.Delete(ctx, b.key(spec, "replay.har"))
			} else {
				err = os.Remove(filepath.Join(b.backend.LocalRoot, b.key(spec, "replay.har")))
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err = b.ReadReport(ctx, receipt)
			if err != nil || !bytes.Equal(got, report) {
				t.Fatalf("status unnecessarily read HAR: %v", err)
			}
			if read, err := b.Read(ctx, receipt); err == nil {
				read.Close()
				t.Fatal("missing native HAR accepted")
			}
			for _, kind := range []string{"staging", "unjoined", "source", "hash", "short", "long", "limit", "canceled"} {
				t.Run(kind, func(t *testing.T) {
					r := receipt
					readCtx := ctx
					switch kind {
					case "staging":
						r.State = "staging"
					case "unjoined":
						r.WriterJoined = false
					case "source":
						r.Spec.SourceSHA256 = strings.Repeat("e", 64)
					case "hash":
						r.Spec.ReportSHA256 = strings.Repeat("e", 64)
					case "short":
						r.Spec.ReportBytes--
					case "long":
						r.Spec.ReportBytes++
					case "limit":
						r.Spec.ReportBytes = 16<<20 + 1
					case "canceled":
						var cancel context.CancelFunc
						readCtx, cancel = context.WithCancel(ctx)
						cancel()
					}
					if got, err := b.ReadReport(readCtx, r); err == nil || got != nil {
						t.Fatalf("invalid report returned: %q %v", got, err)
					}
				})
			}
			// Ensure returning report bytes neither mutates nor consumes the stored report.
			reader, err := b.open(ctx, b.key(spec, "report.json"))
			if err != nil {
				t.Fatal(err)
			}
			retained, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || !bytes.Equal(retained, report) {
				t.Fatal("report changed", err)
			}
		})
	}
}
