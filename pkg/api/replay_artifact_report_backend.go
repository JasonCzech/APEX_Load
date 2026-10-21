package api

import (
	"bytes"
	"context"
	"errors"

	"github.com/jasonczech/apexload/pkg/storage"
)

// ReadReport verifies only the bounded report for status polling. It does not
// assert current availability of the HAR; downloads verify both complete files.
func (b *replayArtifactBackend) ReadReport(ctx context.Context, receipt storage.ReplayArtifactReceipt) ([]byte, error) {
	if receipt.State != "ready" || !receipt.WriterJoined {
		return nil, errors.New("replay artifact is not published")
	}
	if err := b.validate(receipt.Spec); err != nil {
		return nil, err
	}
	reader, err := b.open(ctx, b.key(receipt.Spec, "report.json"))
	if err != nil {
		return nil, err
	}
	var report bytes.Buffer
	copyErr := copyReplayArtifact(ctx, &report, reader, receipt.Spec.ReportBytes, receipt.Spec.ReportSHA256)
	if err = errors.Join(copyErr, reader.Close()); err != nil {
		return nil, err
	}
	if err = b.validate(receipt.Spec); err != nil {
		return nil, err
	}
	return report.Bytes(), nil
}
