package api

import "errors"

const (
	envArchiveConversionMaxBytes     = "APEXLOAD_ARCHIVE_CONVERSION_MAX_BYTES"
	envArchiveConversionMinFreeBytes = "APEXLOAD_ARCHIVE_CONVERSION_MIN_FREE_BYTES"
)

var errArchiveConversionLimit = errors.New("archive conversion byte limit exceeded")
var errArchiveConversionFreeSpace = errors.New("insufficient free space for archive conversion")

func newArchiveConversionBudget(root string) (*archiveOutputBudget, error) {
	limit, err := collectionCacheByteLimit(envArchiveConversionMaxBytes, 32<<30, 128<<30)
	if err != nil {
		return nil, err
	}
	minFree, err := collectionCacheByteLimit(envArchiveConversionMinFreeBytes, 64<<20, 1<<50)
	if err != nil {
		return nil, err
	}
	backlog, err := newArchiveBacklogBudget(root)
	if err != nil {
		return nil, err
	}
	return &archiveOutputBudget{backlog: backlog, root: root, remaining: limit, minFree: minFree, limitErr: errArchiveConversionLimit, freeSpaceErr: errArchiveConversionFreeSpace}, nil
}

func conversionBudget(root string, budgets []*archiveOutputBudget) (*archiveOutputBudget, error) {
	if len(budgets) == 0 {
		return newArchiveConversionBudget(root)
	}
	if len(budgets) != 1 || budgets[0] == nil {
		return nil, errors.New("exactly one archive conversion budget is required")
	}
	return budgets[0], nil
}
