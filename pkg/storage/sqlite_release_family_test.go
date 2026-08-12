package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestProjectReleaseFamilyUsesCanonicalIdentityAndTracksMembers(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	project := &Project{ID: "family-project", Name: "Family project"}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	applications := []*Application{
		{ID: "family-app-a", ProjectID: project.ID, Name: "A"},
		{ID: "family-app-b", ProjectID: project.ID, Name: "B"},
	}
	for _, application := range applications {
		if err := store.SaveApplication(application); err != nil {
			t.Fatalf("SaveApplication(%s): %v", application.ID, err)
		}
	}
	preferred, err := store.CreateProjectReleaseFamily(project.ID, "2608", applications[1].ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateProjectReleaseFamily: %v", err)
	}
	if preferred.ApplicationID != applications[1].ID {
		t.Fatalf("preferred application = %q, want %q", preferred.ApplicationID, applications[1].ID)
	}

	families, err := store.ListReleasesByProject(project.ID, false)
	if err != nil {
		t.Fatalf("ListReleasesByProject: %v", err)
	}
	if len(families) != 1 {
		t.Fatalf("families = %#v, want one", families)
	}
	family := families[0]
	if family.ID != preferred.GroupID || family.GroupID != preferred.GroupID {
		t.Fatalf("canonical family identity = %#v, preferred group %q", family, preferred.GroupID)
	}
	for _, application := range applications {
		memberID := family.MemberIDs[application.ID]
		if memberID == "" {
			t.Fatalf("member id missing for %s: %#v", application.ID, family.MemberIDs)
		}
		member, err := store.GetRelease(memberID)
		if err != nil || member.ApplicationID != application.ID || member.GroupID != family.ID {
			t.Fatalf("member %s = %#v, err=%v", application.ID, member, err)
		}
	}
	resolved, err := store.GetRelease(family.ID)
	if err != nil || resolved.GroupID != family.ID {
		t.Fatalf("GetRelease(canonical) = %#v, err=%v", resolved, err)
	}
}

func TestProjectReleaseFamilyMutationAppliesToInheritedMember(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	project := &Project{ID: "mutation-project", Name: "Mutation project"}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	first := &Application{ID: "mutation-app-a", ProjectID: project.ID, Name: "A"}
	if err := store.SaveApplication(first); err != nil {
		t.Fatalf("SaveApplication(first): %v", err)
	}
	created, err := store.CreateProjectReleaseFamily(project.ID, "Old", first.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateProjectReleaseFamily: %v", err)
	}
	created.Name = "Renamed"
	updated, err := store.UpdateReleaseIfUnchanged(created, created.UpdatedAt)
	if err != nil || !updated {
		t.Fatalf("UpdateReleaseIfUnchanged updated=%v err=%v", updated, err)
	}
	second := &Application{ID: "mutation-app-b", ProjectID: project.ID, Name: "B"}
	if err := store.SaveApplication(second); err != nil {
		t.Fatalf("SaveApplication(second): %v", err)
	}
	releases, err := store.ListReleases(second.ID, false)
	if err != nil || len(releases) != 1 || releases[0].Name != "Renamed" || releases[0].GroupID != created.GroupID {
		t.Fatalf("inherited releases = %#v, err=%v", releases, err)
	}
	if err := store.ArchiveRelease(created.GroupID); err != nil {
		t.Fatalf("ArchiveRelease(canonical): %v", err)
	}
	for _, application := range []*Application{first, second} {
		archived, err := store.ListReleases(application.ID, true)
		if err != nil || len(archived) != 1 || archived[0].ArchivedAt == nil {
			t.Fatalf("archived releases for %s = %#v, err=%v", application.ID, archived, err)
		}
	}
}

func TestNewApplicationInheritsLegacyReleaseWithoutGroupID(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	project := &Project{ID: "legacy-family-project", Name: "Legacy family project"}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	first := &Application{ID: "legacy-family-app-a", ProjectID: project.ID, Name: "A"}
	if err := store.SaveApplication(first); err != nil {
		t.Fatalf("SaveApplication(first): %v", err)
	}
	legacy := &Release{
		ID:            "legacy-release",
		ApplicationID: first.ID,
		ProjectID:     project.ID,
		Name:          "Imported Legacy",
	}
	if err := store.SaveRelease(legacy); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	second := &Application{ID: "legacy-family-app-b", ProjectID: project.ID, Name: "B"}
	if err := store.SaveApplication(second); err != nil {
		t.Fatalf("SaveApplication(second): %v", err)
	}
	releases, err := store.ListReleases(second.ID, false)
	if err != nil {
		t.Fatalf("ListReleases(second): %v", err)
	}
	if len(releases) != 1 || releases[0].Name != legacy.Name || releases[0].GroupID != legacy.ID {
		t.Fatalf("inherited legacy releases = %#v, want one member of family %q", releases, legacy.ID)
	}
}
