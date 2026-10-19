package storage

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/elastic"
)

// A write hands its caller the row's new updated_at, and clients send that
// back as the version their next write expects. These stores compare it
// exactly with the stored row, which PostgreSQL keeps at microseconds while
// Go's clock has nanoseconds on Linux. Each exercise runs on SQLite here and
// on PostgreSQL in updated_at_precision_postgres_integration_test.go.
var updatedAtPrecisionExercises = []struct {
	name string
	run  func(*testing.T, *SQLiteStore)
}{
	{"cloud_connection", exerciseCloudConnectionVersion},
	{"cluster_connection", exerciseClusterConnectionVersion},
	{"execution_target", exerciseExecutionTargetVersion},
	{"domain_deployment_target", exerciseDomainDeploymentTargetVersion},
	{"elk_connection", exerciseELKConnectionVersion},
	{"dynatrace_connection", exerciseDynatraceConnectionVersion},
	{"project", exerciseProjectVersion},
	{"application", exerciseApplicationVersion},
	{"release", exerciseReleaseVersion},
	{"release_family", exerciseReleaseFamilyVersion},
	{"fleet_pool", exerciseFleetPoolVersion},
	{"object_repository", exerciseObjectRepositoryVersion},
	{"test_note", exerciseTestNoteVersion},
	{"test_note_stamped_by_caller", exerciseCallerStampedTestNoteVersion},
}

func TestUpdatedAtPrecision(t *testing.T) {
	for _, tc := range updatedAtPrecisionExercises {
		t.Run(tc.name, func(t *testing.T) {
			store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "updated-at-precision.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			tc.run(t, store)
		})
	}
}

type versionedRow struct {
	// zone is the location the store has always stamped in. SQLite orders
	// rows by their timestamp text, so a store must not switch zones.
	zone *time.Location
	// editsUseDatabaseClock marks a store whose edits take their version from
	// the database clock, or bump the stored version, which pgx reads back in
	// the local zone.
	editsUseDatabaseClock bool
	create                func() error
	// held is the version the caller's copy carries after its last write.
	held   func() time.Time
	stored func() (time.Time, error)
	edit   func(round int, expected time.Time) (bool, error)
}

// exerciseVersionedRow creates a row and edits it twice, each edit
// conditioned on the version the previous write returned.
func exerciseVersionedRow(t *testing.T, row versionedRow) {
	t.Helper()
	if err := row.create(); err != nil {
		t.Fatal(err)
	}
	for round := 0; ; round++ {
		stored, err := row.stored()
		if err != nil {
			t.Fatal(err)
		}
		requireStoredTimestamp(t, fmt.Sprintf("version after write %d", round), row.held(), stored)
		if (round == 0 || !row.editsUseDatabaseClock) && row.held().Location() != row.zone {
			t.Errorf("version after write %d is in %s, want the store's %s", round, row.held().Location(), row.zone)
		}
		if round == 2 {
			return
		}
		if ok, err := row.edit(round+1, row.held()); err != nil || !ok {
			t.Fatalf("edit %d conditioned on the version the last write returned = %t, %v", round+1, ok, err)
		}
	}
}

func exerciseCloudConnectionVersion(t *testing.T, store *SQLiteStore) {
	c := &CloudConnection{ID: "precision-cloud", Name: "Azure", Provider: CloudProviderAzure, AuthMode: CloudAuthAzureIdentity, AzureSubscriptionID: "subscription-1"}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.Local,
		create: func() error { return store.CreateCloudConnection(c) },
		held:   func() time.Time { return c.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetCloudConnection(c.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			c.Name = fmt.Sprintf("Azure %d", round)
			return store.UpdateCloudConnectionIfUnchanged(c, expected)
		},
	})
}

func exerciseClusterConnectionVersion(t *testing.T, store *SQLiteStore) {
	c := &ClusterConnection{ID: "precision-cluster", Name: "Cluster", Kind: ClusterKindInCluster, Namespace: "apexload", TLSMode: ClusterTLSSystemRoots, AuthMode: ClusterAuthInCluster}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.Local,
		create: func() error { return store.CreateClusterConnection(c) },
		held:   func() time.Time { return c.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetClusterConnection(c.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			c.Name = fmt.Sprintf("Cluster %d", round)
			return store.UpdateClusterConnectionIfUnchanged(c, expected)
		},
	})
}

func exerciseExecutionTargetVersion(t *testing.T, store *SQLiteStore) {
	target := &ExecutionTarget{
		ID: "precision-target", Name: "Static", Kind: ExecutionTargetStatic, ConnectivityMode: ConnectivityDirect,
		StandingOwner: CapacityOwnerApex, BurstOwner: CapacityOwnerDisabled,
		ArchiveMode: ArchiveModeControllerPull, Static: &StaticExecutionTargetSpec{Targets: []string{"generator:9090"}},
	}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.Local,
		create: func() error { return store.CreateExecutionTarget(target) },
		held:   func() time.Time { return target.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetExecutionTarget(target.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			target.Name = fmt.Sprintf("Static %d", round)
			return store.UpdateExecutionTargetIfUnchanged(target, expected)
		},
	})
}

func exerciseDomainDeploymentTargetVersion(t *testing.T, store *SQLiteStore) {
	seedDomain(t, store, "precision-domain", "Precision")
	target := &DomainDeploymentTarget{DomainID: "precision-domain", Provider: DeploymentProviderAzureAKS, UpdatedBySubject: "admin"}
	exerciseVersionedRow(t, versionedRow{
		zone: time.Local,
		create: func() error {
			saved, err := store.UpsertDomainDeploymentTargetIfUnchanged(target, time.Time{})
			if err == nil && !saved {
				err = fmt.Errorf("domain deployment target was not created")
			}
			return err
		},
		held: func() time.Time { return target.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetDomainDeploymentTarget(target.DomainID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			target.UpdatedBySubject = fmt.Sprintf("admin %d", round)
			return store.UpsertDomainDeploymentTargetIfUnchanged(target, expected)
		},
	})
}

func exerciseELKConnectionVersion(t *testing.T, store *SQLiteStore) {
	requireProjectFixtures(t, store, "precision-monitor")
	c := &ELKConnection{ID: "precision-elastic", ProjectID: "precision-monitor", Name: "Elastic", Enabled: true, Config: elastic.Config{Endpoint: "https://elastic.example", Product: "elasticsearch", AuthType: "api_key"}, APIKeySecretName: "API_KEY"}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.UTC,
		create: func() error { return store.CreateELKConnection(c) },
		held:   func() time.Time { return c.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetELKConnection(c.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			c.Name = fmt.Sprintf("Elastic %d", round)
			return store.UpdateELKConnectionIfUnchanged(c, expected)
		},
	})
}

func exerciseDynatraceConnectionVersion(t *testing.T, store *SQLiteStore) {
	requireProjectFixtures(t, store, "precision-monitor")
	c := &DynatraceConnection{ID: "precision-dynatrace", ProjectID: "precision-monitor", Name: "Dynatrace", Enabled: true, CollectionMode: "direct", EnvironmentURL: "https://fixture.live.dynatrace.com", PlatformURL: "https://fixture.apps.dynatrace.com", MetricsEnabled: true, EnvironmentTokenSecret: "env", PlatformAuthType: DynatracePlatformAuthToken, PlatformTokenSecret: "platform"}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.UTC,
		create: func() error { return store.CreateDynatraceConnection(c) },
		held:   func() time.Time { return c.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetDynatraceConnection(c.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			c.Name = fmt.Sprintf("Dynatrace %d", round)
			return store.UpdateDynatraceConnectionIfUnchanged(c, expected)
		},
	})
}

func exerciseApplicationVersion(t *testing.T, store *SQLiteStore) {
	application := &Application{ID: "precision-application", ProjectID: DefaultProjectID, Name: "Application"}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.Local,
		create: func() error { return store.SaveApplication(application) },
		held:   func() time.Time { return application.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetApplication(application.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			application.Name = fmt.Sprintf("Application %d", round)
			return store.UpdateApplicationIfUnchanged(application, expected)
		},
	})
}

func exerciseReleaseVersion(t *testing.T, store *SQLiteStore) {
	if err := store.SaveApplication(&Application{ID: "precision-release-application", ProjectID: DefaultProjectID, Name: "Application"}); err != nil {
		t.Fatal(err)
	}
	release := &Release{ID: "precision-release", ApplicationID: "precision-release-application", Name: "Release"}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.Local,
		create: func() error { return store.SaveRelease(release) },
		held:   func() time.Time { return release.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetRelease(release.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			release.Name = fmt.Sprintf("Release %d", round)
			return store.UpdateReleaseIfUnchanged(release, expected)
		},
	})
}

func exerciseFleetPoolVersion(t *testing.T, store *SQLiteStore) {
	cluster := &ClusterConnection{ID: "precision-pool-cluster", Name: "Cluster", Kind: ClusterKindInCluster, Namespace: "apexload", TLSMode: ClusterTLSSystemRoots, AuthMode: ClusterAuthInCluster}
	if err := store.CreateClusterConnection(cluster); err != nil {
		t.Fatal(err)
	}
	pool := &FleetPool{ID: "precision-pool", Name: "Pool", CloudProvider: DeploymentProviderKubernetes, ClusterID: cluster.ID}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.Local,
		create: func() error { return store.SaveFleetPool(pool) },
		held:   func() time.Time { return pool.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetFleetPool(pool.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			pool.Name = fmt.Sprintf("Pool %d", round)
			return store.UpdateFleetPoolIfUnchanged(pool, nil, false, expected)
		},
	})
}

func exerciseTestNoteVersion(t *testing.T, store *SQLiteStore) {
	saveTestNotesRun(t, store, "precision-notes-run", DefaultProjectID)
	note := &TestNote{ID: "precision-note", TestID: "precision-notes-run", Body: "first", CreatedBy: "operator"}
	exerciseVersionedRow(t, versionedRow{
		zone:                  time.UTC,
		editsUseDatabaseClock: true,
		create:                func() error { return store.CreateTestNote(note) },
		held:                  func() time.Time { return note.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetTestNote(note.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			note.Body = fmt.Sprintf("edit %d", round)
			return store.UpdateTestNoteAndArchiveRefreshIfUnchanged(note, expected)
		},
	})
}

// The notes API stamps a new note itself and creates it with its archive
// refresh in one transaction.
func exerciseCallerStampedTestNoteVersion(t *testing.T, store *SQLiteStore) {
	saveTestNotesRun(t, store, "precision-stamped-notes-run", DefaultProjectID)
	note := &TestNote{ID: "precision-stamped-note", TestID: "precision-stamped-notes-run", Body: "first", CreatedBy: "operator", CreatedAt: nanosecondCreatedAt, UpdatedAt: nanosecondCreatedAt}
	exerciseVersionedRow(t, versionedRow{
		zone:                  time.UTC,
		editsUseDatabaseClock: true,
		create:                func() error { return store.CreateTestNoteAndArchiveRefresh(note) },
		held:                  func() time.Time { return note.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetTestNote(note.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			note.Body = fmt.Sprintf("edit %d", round)
			return store.UpdateTestNoteAndArchiveRefreshIfUnchanged(note, expected)
		},
	})
}

// The releases API creates a release in every application of the project and
// stamps it itself, in local time.
func exerciseReleaseFamilyVersion(t *testing.T, store *SQLiteStore) {
	if err := store.SaveApplication(&Application{ID: "precision-family-application", ProjectID: DefaultProjectID, Name: "Application"}); err != nil {
		t.Fatal(err)
	}
	var release *Release
	exerciseVersionedRow(t, versionedRow{
		zone: time.Local,
		create: func() error {
			var err error
			release, err = store.CreateProjectReleaseFamily(DefaultProjectID, "Release", "precision-family-application", nanosecondCreatedAt.In(time.Local))
			return err
		},
		held: func() time.Time { return release.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetRelease(release.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			release.Name = fmt.Sprintf("Release %d", round)
			return store.UpdateReleaseIfUnchanged(release, expected)
		},
	})
}

func exerciseProjectVersion(t *testing.T, store *SQLiteStore) {
	project := &Project{ID: "precision-project", Name: "Project"}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.Local,
		create: func() error { return store.SaveProject(project) },
		held:   func() time.Time { return project.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetProject(project.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			project.Name = fmt.Sprintf("Project %d", round)
			return store.UpdateProjectIfUnchanged(project, expected)
		},
	})
}

// The dashboard keeps the repository a write returns and compares it with the
// listed one before it accepts the listing.
func exerciseObjectRepositoryVersion(t *testing.T, store *SQLiteStore) {
	repo := &ObjectRepository{ID: "precision-repository", ProjectID: DefaultProjectID, Name: "Objects", Objects: []RepositoryObject{{LogicalName: "search", CSS: "#search"}}}
	exerciseVersionedRow(t, versionedRow{
		zone:   time.Local,
		create: func() error { return store.SaveObjectRepository(repo) },
		held:   func() time.Time { return repo.UpdatedAt },
		stored: func() (time.Time, error) {
			got, err := store.GetObjectRepository(repo.ID)
			if err != nil || got == nil {
				return time.Time{}, fmt.Errorf("read the row back: %v", err)
			}
			return got.UpdatedAt, nil
		},
		edit: func(round int, expected time.Time) (bool, error) {
			repo.Name = fmt.Sprintf("Objects %d", round)
			return store.UpdateObjectRepositoryIfUnchanged(repo, expected)
		},
	})
}
