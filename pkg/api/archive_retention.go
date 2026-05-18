package api

import (
	"strings"
	"time"
)

type ArchivePruneCandidate struct {
	TestID        string    `json:"test_id"`
	ArchivePath   string    `json:"archive_path"`
	PolicyName    string    `json:"policy_name,omitempty"`
	ArtifactType  string    `json:"artifact_type"`
	RetentionDays int       `json:"retention_days"`
	CompletedAt   time.Time `json:"completed_at"`
	ExpiredAt     time.Time `json:"expired_at"`
}

func planArchiveRetentionPrune(now time.Time, settings ArchiveRetentionSettings, tests []*ManagedTest) []ArchivePruneCandidate {
	if !settings.Enabled || len(tests) == 0 {
		return nil
	}
	settings.Policies = normalizeArchiveRetentionPolicies(settings.Policies)
	if settings.DefaultRetentionDays <= 0 {
		settings.DefaultRetentionDays = defaultSystemSettings(0).ArchiveRetention.DefaultRetentionDays
	}
	candidates := make([]ArchivePruneCandidate, 0)
	for _, test := range tests {
		if test == nil || test.Archive == nil || strings.TrimSpace(test.Archive.ArchivePath) == "" {
			continue
		}
		completedAt := archiveRetentionCompletedAt(test)
		if completedAt.IsZero() {
			continue
		}
		policy := matchingArchiveRetentionPolicy(settings, test)
		expiredAt := completedAt.Add(time.Duration(policy.RetentionDays) * 24 * time.Hour)
		if now.Before(expiredAt) {
			continue
		}
		candidates = append(candidates, ArchivePruneCandidate{
			TestID:        test.ID,
			ArchivePath:   test.Archive.ArchivePath,
			PolicyName:    policy.Name,
			ArtifactType:  policy.ArtifactType,
			RetentionDays: policy.RetentionDays,
			CompletedAt:   completedAt,
			ExpiredAt:     expiredAt,
		})
	}
	return candidates
}

func archiveRetentionCompletedAt(test *ManagedTest) time.Time {
	if test == nil {
		return time.Time{}
	}
	if test.Archive != nil && !test.Archive.CompletedAt.IsZero() {
		return test.Archive.CompletedAt
	}
	if test.EndedAt != nil {
		return *test.EndedAt
	}
	return time.Time{}
}

func matchingArchiveRetentionPolicy(settings ArchiveRetentionSettings, test *ManagedTest) ArchiveRetentionPolicy {
	for _, policy := range settings.Policies {
		if archiveRetentionPolicyMatches(policy, test) {
			return policy
		}
	}
	return ArchiveRetentionPolicy{
		Name:          "default",
		ArtifactType:  "bundle",
		RetentionDays: settings.DefaultRetentionDays,
	}
}

func archiveRetentionPolicyMatches(policy ArchiveRetentionPolicy, test *ManagedTest) bool {
	if test == nil {
		return false
	}
	if policy.ProjectID != "" && policy.ProjectID != test.ProjectID {
		return false
	}
	if policy.ReleaseID != "" && policy.ReleaseID != test.ReleaseID {
		return false
	}
	if policy.RunStatus != "" && !strings.EqualFold(policy.RunStatus, test.Status) {
		return false
	}
	if policy.RunType != "" && policy.RunType != test.ScenarioMode {
		return false
	}
	return true
}
