package storage

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

const schemaMigrationLedgerDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	fingerprint TEXT NOT NULL,
	applied_at DATETIME NOT NULL
)`

type schemaRelease struct {
	Version     int
	Name        string
	Fingerprint string
}

// schemaReleases is append-only. A schema DDL, optional migration, index, or
// named convergence phase change must add a release; changing a released
// fingerprint makes existing databases fail closed instead of claiming that a
// different schema is the same version.
var schemaReleases = []schemaRelease{
	{Version: 1, Name: "idempotent-schema-baseline", Fingerprint: "60c5d967d6f0543361fa8842fac8a0e157b118bcef49d2c6310a7cee19a15832"},
	{Version: 2, Name: "cloudtest-outcome-parity-foundations", Fingerprint: "9a70267c0b98361c2563e0339c17b3eaa1d5595ef6fea06b9f94eb62c696fb28"},
	{Version: 3, Name: "shared-dashboard-definitions", Fingerprint: "59f38c09909ddb93e6e1ad362595a5436a9942fe8befd861ba9ed4636759d3ad"},
	{Version: 4, Name: "domain-feature-access-controls", Fingerprint: "86c06ad55fe5e62f1e8e9956738a8b42080e5a63988c9b2903e1f03b9eb0a13a"},
	{Version: 5, Name: "production-traffic-profiles", Fingerprint: "6d3bff068cfb95b5ac82283cc3794af6941f3d3f9df9c4c329d3fdc9a2133ada"},
	{Version: 6, Name: "production-traffic-run-snapshots", Fingerprint: "b77be774c8f129230d348c391e3cf34ab356f7900446bf9244f09e3d852ff462"},
	{Version: 7, Name: "scenario-group-transaction-response-time-sla", Fingerprint: "2b27fdaf2af24996243a8aa5d67e97b01cbe81515ce391653da7f744df80ad2e"},
	{Version: 8, Name: "peak-pod-capacity-reservations", Fingerprint: "3c72a2e09f9362c4167a2f804a8c3d1b8a275bf96ce98822705f67d1ef4b11a1"},
	{Version: 9, Name: "durable-monitoring-threshold-incidents", Fingerprint: "0f74cef209dd4dfda0093ec2c27fb7cd581b66875ca1549aa88966720d660232"},
	{Version: 10, Name: "library-application-release-assignments", Fingerprint: "9ca4de0a07a29ffe6bd32864699ecf0a6147fc4ec5228a0ad276db246f831d7b"},
	{Version: 11, Name: "optional-elk-monitoring", Fingerprint: "32ec84499a06c93dc1a7ac86bf962134caf4efb5ce4019c4f4e101a3cf17f97d"},
	{Version: 12, Name: "optional-dynatrace-saas-monitoring", Fingerprint: "47b102255d607b776d1cda7d3e4fc0c79cb978a4ed991a9e0bac18ae80008daf"},
	{Version: 13, Name: "explicit-iteration-termination", Fingerprint: "2b3f09f8f1be8fedd664b88d5815556071936438f749f9c06e9d4d15e8db5273"},
	{Version: 14, Name: "confluence-cloud-result-publishing", Fingerprint: "dcc18fcf8fd11ff340bf1fedb505d5c642ecda83e7909b5b23f8ea6fb055cdba"},
	{Version: 15, Name: "baseline-comparison-workspace", Fingerprint: "958e28dac8c5e9c668ce79322e2e72a3a590f2bba64808de8830d2eeaa0a1e15"},
	{Version: 16, Name: "ha-run-continuity-checkpoints", Fingerprint: "9f50fe7c91b938c6e590e79b3079ea8e8509c12a7707e60c689a90a76cadb4d1"},
	{Version: 17, Name: "durable-run-archive-collection-failures", Fingerprint: "658982a6cc5a08e06b9d565b2b7cd51f8887eae525c3893309aec364164d682e"},
	{Version: 18, Name: "durable-archive-manifest-preparation", Fingerprint: "5dae09fe85185355f15b1206033dc147f87842b1e005d81fbbe87ec09d78196d"},
	{Version: 19, Name: "durable-archive-collection-sources", Fingerprint: "876b8e155f956851ead8d8a726f3603e4111ac06e3b822a62fe692af02bc8ddf"},
	{Version: 20, Name: "bounded-archive-collection-cache", Fingerprint: "da3695e71768ed2528b0402fb4dedf2dd72bb4463c4351cbfbe1170259906bfd"},
	{Version: 21, Name: "pending-archive-source-replica-holds", Fingerprint: "9d57e08247d40080f7d5b168e81036bfbef2021a22bb0fa8ed36836f43bc6302"},
	{Version: 22, Name: "application-scoped-secrets", Fingerprint: "fb7bef557df89eb2fc02e545fb1685841e1720c5bf2f3dd518682b647da306a3"},
	{Version: 23, Name: "analysis-workflows", Fingerprint: "1c2768f2c961fa5da1d9340e07c868862433ba4c9012f1212420680aee4aad98"},
	{Version: 24, Name: "native-interval-evidence", Fingerprint: "6c0d32e793c1de36cc7089696093f731d5ac200e1823f84c90933c96673c43f5"},
	{Version: 25, Name: "run-timeline-annotations", Fingerprint: "f4dcd23df64db606ba53343accd751975904ae7eb338eb657342fe72369fb9d2"},
	{Version: 26, Name: "retained-drop-counter-availability", Fingerprint: "364669cc553fcaa50eb2af5453de91428115c016fa47232c647175808c335dca"},
	{Version: 27, Name: "object-activity-and-archives", Fingerprint: "10e70e6ffc7829b2d9e594c854903b1db57d4807b1c2878cea751e0eeb37d1d2"},
	{Version: 28, Name: "archive-restoration-schedule-boundary", Fingerprint: "ba1ac64df70d019c92b5aaae2e4d6ed14f927d8fa4b30d9083a1f533e71981ed"},
	{Version: 29, Name: "appdynamics-durable-run-monitoring", Fingerprint: "ba1618a00f60ea7b120e07b375d67d8fd7c6368d1a9e8a18ef25c09f9b4c0186"},
	{Version: 30, Name: "prometheus-governed-run-monitoring", Fingerprint: "c1393b724be106974e1c3e9765668d426dbd6720dc6df89096b7a32ce0d0124f"},
	{Version: 31, Name: "durable-messaging-progress-history", Fingerprint: "7fbd77d1e116e4349e96ca211376035d847db90d8137c640e7d8f82cb2912f32"},
	{Version: 32, Name: "durable-messaging-management-polls", Fingerprint: "8302b2875bbd64d91edac96ab4c39b6adf66bbfb288f60fbef5428718a1d4911"},
	{Version: 33, Name: "isolated-native-analysis-attempts", Fingerprint: "348e5bff6128c23431046d91f301e65f219eaa542faead33fc42d1508c4587df"},
	{Version: 34, Name: "durable-analysis-upload-physical-writers", Fingerprint: "52971cd3c20e72aefeb2c719f02e125a3689f107a50981c4da14d01a8e34783b"},
	{Version: 35, Name: "dynatrace-saas-durable-run-monitoring", Fingerprint: "de996ade9928b95fa97e6ede0c811d2ee20ee73e0f44d1e26945547814066d7a"},
	{Version: 36, Name: "durable-script-goal-config", Fingerprint: "2d937708f297ade2cdcdc224678c7f25689d3f5f923124549963014c38e0b29e"},
	{Version: 37, Name: "durable-script-content-revision", Fingerprint: "d741b8384d281d84172d60b888a72228892173bbbc96d4f04a0e480cc1e9b745"},
	{Version: 38, Name: "durable-scenario-content-revision", Fingerprint: "185b01e0a6e99c3a5d43a668d2113d7258b5cd89aa0f30e45f094bb2f9c7f5d4"},
	{Version: 39, Name: "run-trace-context-policy", Fingerprint: "26cd04b1928418d383f28268ae70c20e2b943e08217c7fdb3855e8a092a6d8cc"},
	{Version: 40, Name: "rca-analysis-revisions", Fingerprint: "0aa6b5258f7162db82e7647c622672b145dd1db69356907c774882a4071a54e3"},
	{Version: 41, Name: "business-key-project-keys", Fingerprint: "385585ff3818a833a350cd25838901726297b40a458670abf302d52bbb667c52"},
	{Version: 42, Name: "rca-critiques", Fingerprint: "b17ff1432f5b81961297d33644433a760017d6bf8317877509633fab06ac70bb"},
	{Version: 43, Name: "elastic-provider-monitoring", Fingerprint: "9151ca56509c9194e7eb425bb8f4f9f243f04400388b04d86f26151f0dfc744a"},
	{Version: 44, Name: "provider-evidence-searchable-metadata", Fingerprint: "8674a06ddd84e1e571ed5fa760b3affbff4453b30c076ca7591918d0b5e68c37"},
	// Releases 45-51 were 43-49 on the feature completion branch; they were
	// renumbered when it merged after main's 43 and 44 had shipped.
	{Version: 45, Name: "authoring-mutation-receipts", Fingerprint: "f5162d40f9f1292edc151854823456d291ec91e5843c48f3faea27936bc108c9"},
	{Version: 46, Name: "feature-evaluation-jobs", Fingerprint: "8a3b85bc69d51fc99ef82b036f5dd3095c959feccd968268eddd2b3b0b06da85"},
	{Version: 47, Name: "feature-acceptance-records", Fingerprint: "6e18988c1ecd91779b20dd7d0176c5676e1ca8d85b17b7f4bb95ee808f693aac"},
	{Version: 48, Name: "evaluation-job-request-digest-attempts", Fingerprint: "e0a9875257c2eec52d7e3db93e37e93337696235d5cd1a3df62ab7077dd692be"},
	{Version: 49, Name: "feature-acceptance-record-sequence", Fingerprint: "97e0b12e5e5f458ee14262ff22bba3876a8128ff73cdd02cbd6c96fdfd495244"},
	{Version: 50, Name: "evaluation-job-result-bytes", Fingerprint: "be98302163e49efb45a5d716d080b4f48f413493aab1bf3c426864a34f8efaad"},
	{Version: 51, Name: "run-evidence-receipts", Fingerprint: "4de7650fd22ebc9bd5bbc873fa909fd9583056c0436d2514256c6398719433bc"},
	{Version: 52, Name: "joint-evidence-projection", Fingerprint: "14d07c2e8700df08d5914fc2c100674ccef500fda15343abd335ea56c35ad9b3"},
	{Version: 53, Name: "evaluation-job-subject", Fingerprint: "c09bb47991735e9b36684bea7c0cb95e68abc177f57adaf6b8f5354c797dfcb2"},
	{Version: 54, Name: "dashboard-definition-revisions", Fingerprint: "a4489a4353666e183fdb02597b00bdb9d5eae844766f9b2a87f1807d04ad7233"},
	{Version: 55, Name: "rca-analysis-origin", Fingerprint: "794d179a7c421744813b6404fd11d62e37c359d1297816affe8373f5364fefeb"},
	{Version: 56, Name: "reverse-generator-target-ownership", Fingerprint: "32550b77652a06cbc16494068759f548a88c8b718acafc0f65a2afbd68669a21"},
	{Version: 57, Name: "run-scoped-live-data-tables", Fingerprint: "fa414da8e781052f93d25b2bbb6487cf02b4acfbb389818a3acaf98044fa1c9b"},
	{Version: 58, Name: "project-live-data-tables", Fingerprint: "105182f654cc8cae4c5bb1a65dda94484d2cbf51e9fd889a7c8d1051b4e6e9d0"},
	{Version: 59, Name: "distributed-rendezvous-outcomes", Fingerprint: "82b6b57eda589317b2edd663d6107e94d398915425011e2bc30420548e08133b"},
	{Version: 60, Name: "execution-plan-release-overlap-run-link", Fingerprint: "82da9011bb48e4da2a3ce95271a71994bd96e99977b578401677122012746567"},
	{Version: 61, Name: "data-pool-column-order", Fingerprint: "9caa31d488e14bcf56f24c3f132115228103051f8686ec45de56f1806859a33b"},
	{Version: 62, Name: "scenario-drafts", Fingerprint: "237838e015ad61c6e521a6af793e7cb5193528938640b54b1a0d63a17aba52cc"},
	{Version: 63, Name: "run-execution-generation", Fingerprint: "0a2a6b72b8c7c8e16929b4cbe989d0dc8a572ae2651efc6ecc7a12d5f3c2f7a4"},
	{Version: 64, Name: "object-activity-summary-index", Fingerprint: "c3c6c6508b2ffdc3942207a5419b82773cb4d2c8b880fd3665769f98014f6255"},
	{Version: 65, Name: "audit-event-insertion-order", Fingerprint: "642a661c427abb0b78aeb6396a86d0e94ccd1cf5a33b2724314b8986aa8eadae"},
	{Version: 66, Name: "object-activity-insertion-order", Fingerprint: "3e407f1ddff5f098166d724dfef728cee82a893eb38b52fc083681dc9b9af7e5"},
	{Version: 67, Name: "durable-telemetry-checkpoint", Fingerprint: "6e53190738516893c37874c83619953523d9cd18d05aba2bbf5a36aac3be3d32"},
	{Version: 68, Name: "durable-telemetry-notification-outbox", Fingerprint: "be3b4004e8c8291b487a00536d736aa46e57759bcbbaf55daf74cb7e34d04869"},
	{Version: 69, Name: "atomic-telemetry-notification-publication", Fingerprint: "da725565a48cf8671ff9df9b7d2083e51cd888c4ae5cad553d1efb1178ce5deb"},
	{Version: 70, Name: "durable-java-http-action-history", Fingerprint: "e1cfdf7cb8281eb9236da48ff4df94cdab8e72f9da16e18e2de36d414662cd9d"},
	{Version: 71, Name: "deleted-test-identities", Fingerprint: "eb3e5fd1f6988061c86d0c4c50dee89c0c3853732dddd91c1439e9cc43f0fd5c"},
	{Version: 72, Name: "browser-acceptance-dependency-pins", Fingerprint: "8738458dd436ce260ff34e9f7640762e22b1df5cad6c02a38884bd91e81a363d"},
	{Version: 73, Name: "run-analysis-policy-selections", Fingerprint: "c930085449d7bd3b0eec530963d9cea06b50b9c27090a2c91259f93f8e7abd4a"},
	{Version: 74, Name: "replay-artifact-retention-receipts", Fingerprint: "14cc8a74dc9d9fdc2091cb8da3c42d6b69e6286d4c3613e821403dfe48f28196"},
	{Version: 75, Name: "browser-baseline-candidate-inventory", Fingerprint: "3b53081becf3ec7b776039bda59d7ffcfe3cf6f1093d3e97a1c103172dc21233"},
}

type SchemaMigrationRecord struct {
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	AppliedAt   time.Time `json:"applied_at"`
}

type SchemaStatus struct {
	CurrentVersion int                     `json:"current_version"`
	AppliedVersion int                     `json:"applied_version"`
	Applied        []SchemaMigrationRecord `json:"applied"`
	Pending        []SchemaMigrationRecord `json:"pending"`
}

func currentSchemaRelease() schemaRelease {
	return schemaReleases[len(schemaReleases)-1]
}

func currentSchemaVersionLabel() string {
	return fmt.Sprintf("apexload.schema.v%d", currentSchemaRelease().Version)
}

func schemaConvergenceFingerprint() string {
	hash := sha256.New()
	write := func(kind, name, query string) {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", kind, name, strings.TrimSpace(query))
	}
	write("ledger", "schema_migrations", schemaMigrationLedgerDDL)
	tableIndex := 0
	for _, query := range schemaCreateTableStatements() {
		if query == schemaMigrationLedgerDDL {
			continue
		}
		write("table", fmt.Sprintf("%06d", tableIndex), query)
		tableIndex++
	}
	for _, migration := range schemaColumnMigrations() {
		write("column", migration.name, migration.query)
	}
	for _, index := range schemaIndexes {
		write("index", index.name, index.query)
	}
	// These named phases mutate or constrain schema state outside the three SQL
	// registries above. Names are intentionally fingerprinted so adding/removing
	// a phase requires a schema release even when its SQL is built dynamically.
	for _, phase := range []string{
		"compatible-import-physical-layout",
		"postgres-artifact-size-bigint",
		"kubernetes-execution-target-backfill",
		"execution-target-enrollment-expiry-backfill",
		"postgres-scheduler-generation-bigint",
		"postgres-audit-event-insert-seq",
		"postgres-object-activity-insert-seq",
		"release-group-backfill",
		"scheduler-content-revision",
		"start-admission-revision",
		"data-pool-paging-state",
		"legacy-import-foreign-key-repair",
		"sanitized-source-availability",
		"github-check-link-kind",
		"blank-script-id",
		"legacy-script-recording-repair",
		"correlation-ruleset-repair",
		"archive-finalization-intent-token",
		"github-remote-blob-sha",
		"github-sync-target-owner",
		"fleet-pool-membership-consolidation",
		"interval-and-archive-schema-version",
		"default-domain-project-application",
		"postgres-foreign-keys",
		"timescaledb",
		"object-activity-legacy-backfill",
		"durable-script-content-revision-immediate-tracking",
		"durable-scenario-content-revision-immediate-tracking",
		"live-table-optional-seed-binding",
	} {
		write("phase", phase, "")
	}
	for _, trigger := range sqliteScriptRevisionTriggers() {
		write("script-revision-sqlite-trigger", trigger.name, trigger.sql)
	}
	for _, function := range postgresScriptRevisionFunctions() {
		write("script-revision-postgres-function", function.name, postgresScriptRevisionFunctionSQL(function))
	}
	for _, trigger := range postgresScriptRevisionTriggers() {
		write("script-revision-postgres-trigger", trigger.name, postgresScriptRevisionTriggerSQL(trigger))
	}

	for _, trigger := range sqliteScenarioRevisionTriggers() {
		write("scenario-revision-sqlite-trigger", trigger.name, trigger.sql)
	}
	for _, function := range postgresScenarioRevisionFunctions() {
		write("scenario-revision-postgres-function", function.name, postgresScenarioRevisionFunctionSQL(function))
	}
	for _, trigger := range postgresScenarioRevisionTriggers() {
		write("scenario-revision-postgres-trigger", trigger.name, postgresScenarioRevisionTriggerSQL(trigger))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (s *SQLiteStore) ensureSchemaMigrationLedger() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage is not initialized")
	}
	if _, err := s.db.Exec(schemaMigrationLedgerDDL); err != nil {
		return fmt.Errorf("create schema migration ledger: %w", err)
	}
	status, err := s.SchemaStatus()
	if err != nil {
		return err
	}
	current := currentSchemaRelease()
	if status.AppliedVersion > current.Version {
		return fmt.Errorf("database schema version %d is newer than supported version %d", status.AppliedVersion, current.Version)
	}
	for index, release := range schemaReleases {
		if release.Version != index+1 || strings.TrimSpace(release.Name) == "" || strings.TrimSpace(release.Fingerprint) == "" {
			return fmt.Errorf("schema release registry must be contiguous and complete: index %d has version %d", index, release.Version)
		}
	}
	for _, applied := range status.Applied {
		if applied.Version <= 0 || applied.Version > len(schemaReleases) {
			return fmt.Errorf("schema migration ledger contains unknown version %d", applied.Version)
		}
		expected := schemaReleases[applied.Version-1]
		if applied.Name != expected.Name || applied.Fingerprint != expected.Fingerprint {
			return fmt.Errorf("schema migration %d identity mismatch: found %q/%s, want %q/%s", applied.Version, applied.Name, applied.Fingerprint, expected.Name, expected.Fingerprint)
		}
	}
	return nil
}

func (s *SQLiteStore) recordSchemaReleases() error {
	for _, release := range schemaReleases {
		if _, err := s.db.Exec(`INSERT INTO schema_migrations (version, name, fingerprint, applied_at)
			VALUES (?, ?, ?, ?) ON CONFLICT(version) DO NOTHING`, release.Version, release.Name, release.Fingerprint, time.Now().UTC()); err != nil {
			return fmt.Errorf("record schema migration %d: %w", release.Version, err)
		}
	}
	status, err := s.SchemaStatus()
	if err != nil {
		return err
	}
	if len(status.Pending) != 0 {
		return fmt.Errorf("schema convergence left %d migration releases pending", len(status.Pending))
	}
	for _, applied := range status.Applied {
		expected := schemaReleases[applied.Version-1]
		if applied.Name != expected.Name || applied.Fingerprint != expected.Fingerprint {
			return fmt.Errorf("schema migration %d changed after it was applied", applied.Version)
		}
	}
	return nil
}

// SchemaStatus inspects the ordered migration ledger without mutating it.
func (s *SQLiteStore) SchemaStatus() (SchemaStatus, error) {
	status := SchemaStatus{CurrentVersion: currentSchemaRelease().Version}
	if s == nil || s.db == nil {
		return status, fmt.Errorf("storage is not initialized")
	}
	rows, err := s.db.Query(`SELECT version, name, fingerprint, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return status, fmt.Errorf("read schema migration ledger: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var record SchemaMigrationRecord
		if err := rows.Scan(&record.Version, &record.Name, &record.Fingerprint, &record.AppliedAt); err != nil {
			return status, fmt.Errorf("scan schema migration ledger: %w", err)
		}
		status.Applied = append(status.Applied, record)
		if record.Version > status.AppliedVersion {
			status.AppliedVersion = record.Version
		}
	}
	if err := rows.Err(); err != nil {
		return status, fmt.Errorf("read schema migration ledger: %w", err)
	}
	appliedVersions := make(map[int]bool, len(status.Applied))
	for _, applied := range status.Applied {
		appliedVersions[applied.Version] = true
	}
	for _, release := range schemaReleases {
		if !appliedVersions[release.Version] {
			status.Pending = append(status.Pending, SchemaMigrationRecord{
				Version: release.Version, Name: release.Name, Fingerprint: release.Fingerprint,
			})
		}
	}
	return status, nil
}

func (s *PostgresStore) SchemaStatus() (SchemaStatus, error) {
	if s == nil || s.SQLiteStore == nil {
		return SchemaStatus{}, fmt.Errorf("postgres store is not initialized")
	}
	return s.SQLiteStore.SchemaStatus()
}
