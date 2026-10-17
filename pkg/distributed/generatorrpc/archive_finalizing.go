package generatorrpc

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ArchiveFinalizingMessage is the status message a generator returns for a
// test whose archive it is still writing: the test is running or stopping, so
// its archive will exist shortly. Collectors retry it until their deadline;
// older ones see an Unavailable source, as they did for a missing archive.
const ArchiveFinalizingMessage = "archive is still being finalized"

// IsArchiveFinalizing reports whether err is a generator saying the requested
// archive is still being written.
func IsArchiveFinalizing(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.Unavailable && st.Message() == ArchiveFinalizingMessage
}

// archiveStillFinalizing reports whether testID's archive is about to exist:
// its stop has begun, and stopping ends only after the archive is finalized and
// registered. A test still running answers NotFound, as its archive will not
// exist until something stops it.
func (s *Service) archiveStillFinalizing(testID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lifecycleCoordinator == nil {
		return false
	}
	stopping := s.lifecycleCoordinator.isStoppingLocked(testID)
	return stopping
}

func archiveNotRetainedError(s *Service, testID string) error {
	if s.archiveStillFinalizing(testID) {
		return status.Error(codes.Unavailable, ArchiveFinalizingMessage)
	}
	return status.Error(codes.NotFound, "archive not found")
}
