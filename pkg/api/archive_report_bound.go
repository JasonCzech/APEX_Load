package api

import (
	"errors"
	"fmt"
)

// errArchiveReportBound marks a report build stopped by one of the report's
// own bounds (dimensions, aggregate cells, bounded analysis size). The
// archive alone decides it, so building again gives the same error.
var errArchiveReportBound = errors.New("archive report bound")

// archiveReportBoundError carries a bound's message unchanged while
// matching errArchiveReportBound under errors.Is.
type archiveReportBoundError struct{ message string }

func (e *archiveReportBoundError) Error() string        { return e.message }
func (e *archiveReportBoundError) Is(target error) bool { return target == errArchiveReportBound }

// archiveReportBoundErrorf formats a report-bound error.
func archiveReportBoundErrorf(format string, args ...any) error {
	return &archiveReportBoundError{message: fmt.Sprintf(format, args...)}
}
