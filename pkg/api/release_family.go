package api

import (
	"errors"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

type resolvedReleaseFamily struct {
	CanonicalID string
	ProjectID   string
	Name        string
	Members     []*storage.Release
}

func (family resolvedReleaseFamily) memberIDs() []string {
	ids := make([]string, 0, len(family.Members))
	seen := make(map[string]struct{}, len(family.Members))
	for _, member := range family.Members {
		if member == nil || strings.TrimSpace(member.ID) == "" {
			continue
		}
		if _, ok := seen[member.ID]; ok {
			continue
		}
		seen[member.ID] = struct{}{}
		ids = append(ids, member.ID)
	}
	return ids
}

func (s *Server) resolveReleaseFamily(releaseID string) (resolvedReleaseFamily, error) {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return resolvedReleaseFamily{}, errors.New("release id is required")
	}
	if s.store == nil {
		return resolvedReleaseFamily{CanonicalID: releaseID, ProjectID: storage.DefaultProjectID, Name: releaseID}, nil
	}
	release, err := s.store.GetRelease(releaseID)
	if err != nil || release == nil {
		return resolvedReleaseFamily{}, errors.New("release not found")
	}
	members := []*storage.Release{release}
	if lister, ok := s.store.(releaseFamilyMemberLister); ok {
		listed, listErr := lister.ListReleaseFamilyMembers(releaseID)
		if listErr != nil {
			return resolvedReleaseFamily{}, listErr
		}
		members = listed
	}
	canonicalID := firstNonEmpty(release.GroupID, release.ID)
	projectID := normalizeProjectID(release.ProjectID)
	name := release.Name
	for _, member := range members {
		if member == nil {
			continue
		}
		if projectID == "" {
			projectID = normalizeProjectID(member.ProjectID)
		}
		if name == "" {
			name = member.Name
		}
	}
	return resolvedReleaseFamily{
		CanonicalID: canonicalID,
		ProjectID:   projectID,
		Name:        name,
		Members:     members,
	}, nil
}

func (s *Server) listReleaseFamilyTests(family resolvedReleaseFamily) []*ManagedTest {
	memberIDs := family.memberIDs()
	if len(memberIDs) == 0 {
		memberIDs = []string{family.CanonicalID}
	}
	tests := make([]*ManagedTest, 0)
	seen := make(map[string]struct{})
	for _, releaseID := range memberIDs {
		for _, test := range s.runner.ListTestSummariesByContext("", releaseID, "") {
			if test == nil {
				continue
			}
			if _, ok := seen[test.ID]; ok {
				continue
			}
			seen[test.ID] = struct{}{}
			tests = append(tests, test)
		}
	}
	return tests
}
