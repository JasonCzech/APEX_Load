package api

import "errors"

const (
	envArchivePublicationMaxBytes     = "APEXLOAD_ARCHIVE_PUBLICATION_MAX_BYTES"
	envArchivePublicationMinFreeBytes = "APEXLOAD_ARCHIVE_PUBLICATION_MIN_FREE_BYTES"
)

var errArchivePublicationLimit = errors.New("archive publication copy byte limit exceeded")
var errArchivePublicationFreeSpace = errors.New("insufficient free space for archive publication copy")

// One frozen allowance covers every artifact and the commit marker copied from
// the immutable reader cache into a private publication attempt. It does not
// reserve the maximum up front or remove an unconfirmed source under pressure.
func newArchivePublicationBudget(root string) (*archiveOutputBudget, error) {
	limit, err := collectionCacheByteLimit(envArchivePublicationMaxBytes, 64<<30, 128<<30)
	if err != nil {
		return nil, err
	}
	minFree, err := collectionCacheByteLimit(envArchivePublicationMinFreeBytes, 64<<20, 1<<50)
	if err != nil {
		return nil, err
	}
	backlog, err := newArchiveBacklogBudget(root)
	if err != nil {
		return nil, err
	}
	return &archiveOutputBudget{backlog: backlog, root: root, remaining: limit, minFree: minFree, limitErr: errArchivePublicationLimit, freeSpaceErr: errArchivePublicationFreeSpace}, nil
}
