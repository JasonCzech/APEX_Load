package generatorrpc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/archivequota"
)

const envGeneratorArchiveBacklogMaxBytes = "APEXLOAD_GENERATOR_ARCHIVE_BACKLOG_MAX_BYTES"

func newGeneratorArchiveBacklog(root *os.Root) (*archivequota.Budget, error) {
	limit := int64(512 << 30)
	if raw := strings.TrimSpace(os.Getenv(envGeneratorArchiveBacklogMaxBytes)); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 || parsed > 1<<50 {
			return nil, fmt.Errorf("%s must be positive integer bytes up to 1 PiB", envGeneratorArchiveBacklogMaxBytes)
		}
		limit = parsed
	}
	return archivequota.New(root, archivequota.Options{MaxBytes: limit})
}

func closeGeneratorArchiveBacklog(budget *archivequota.Budget) error {
	if budget == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return budget.Close(ctx)
}

func (w *archiveRecorder) closeFinalizedBacklog() {
	if w.byteBudget == nil || w.byteBudget.backlog == nil {
		return
	}
	if err := closeGeneratorArchiveBacklog(w.byteBudget.backlog); err != nil {
		// The completed source is durable. Failure to retire its conservative
		// reservation must not delete that source or advertise a failed execution.
		slog.Warn("generator archive backlog reservation retained after finalization", "test_id", w.manifest.TestID, "error", err)
	}
	w.byteBudget.backlog = nil
}
