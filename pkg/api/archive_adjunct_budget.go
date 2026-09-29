package api

import (
	"context"
	"errors"
)

const (
	envArchiveAdjunctMaxBytes     = "APEXLOAD_ARCHIVE_ADJUNCT_MAX_BYTES"
	envArchiveAdjunctMinFreeBytes = "APEXLOAD_ARCHIVE_ADJUNCT_MIN_FREE_BYTES"
)

var errArchiveAdjunctLimit = errors.New("archive adjunct output byte limit exceeded")
var errArchiveAdjunctFreeSpace = errors.New("insufficient free space for archive adjunct output")

// One attempt shares its allowance across prepared-manifest recovery, test notes,
// execution metadata and the replacement archive manifest. JSON serialization
// memory, streamed report exports and aggregate retained backlog are separate.
func newArchiveAdjunctBudget(ctx context.Context, root string) (*archiveOutputBudget, error) {
	limit, err := collectionCacheByteLimit(envArchiveAdjunctMaxBytes, 64<<20, 1<<30)
	if err != nil {
		return nil, err
	}
	minFree, err := collectionCacheByteLimit(envArchiveAdjunctMinFreeBytes, 64<<20, 1<<50)
	if err != nil {
		return nil, err
	}
	return &archiveOutputBudget{root: root, remaining: limit, minFree: minFree, limitErr: errArchiveAdjunctLimit, freeSpaceErr: errArchiveAdjunctFreeSpace, ctx: ctx}, nil
}
