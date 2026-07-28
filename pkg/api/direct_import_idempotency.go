package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	directImportDigestKeySetting      = "security.direct_import_digest_hmac_key.v1"
	directImportDigestKeyBytes        = 32
	jmeterDirectImportOperationKey    = "direct_import_operation_id_v1"
	directImportRequestDigestDomain   = "apexload:direct-import:request:v1"
	directImportOperationDigestDomain = "apexload:direct-import:operation:v1"
	directImportScriptDigestDomain    = "apexload:direct-import:script:v1"
)

// A stale receipt owner must never be replaced while its write-ahead library
// claims may still be live. Otherwise the retry can adopt deterministic assets
// and a later claim recovery can mistake those committed assets for the old
// partial transaction. Keep the receipt fence beyond the claim lease, including
// a bounded scheduling/clock margin.
func directImportReceiptLease() time.Duration {
	claimLease := libraryImportClaimTTL()
	margin := claimLease / 10
	if margin < 100*time.Millisecond {
		margin = 100 * time.Millisecond
	}
	if margin > 30*time.Second {
		margin = 30 * time.Second
	}
	return claimLease + margin
}

type directImportAttempt struct {
	store      storage.DirectImportReceiptStore
	receipt    *storage.DirectImportReceipt
	ownerToken string
	created    bool
}

func directImportOperationHash(projectID, sourceProfile, operationID string) string {
	return directImportSHA256(
		normalizeProjectID(projectID),
		strings.TrimSpace(sourceProfile),
		strings.TrimSpace(operationID),
	)
}

func directImportSHA256(parts ...string) string {
	digest := sha256.New()
	for _, part := range parts {
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(part))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func deterministicDirectImportAssetID(operationHash, kind string, ordinal int) string {
	return uuid.NewSHA1(
		uuid.NameSpaceURL,
		[]byte(fmt.Sprintf("apexload:direct-import:%s:%s:%d", operationHash, kind, ordinal)),
	).String()
}

func (s *Server) claimDirectImportAttempt(
	projectID, sourceProfile, operationID, requestSHA256 string,
) (*directImportAttempt, bool, error) {
	store, ok := s.store.(storage.DirectImportReceiptStore)
	if !ok || strings.TrimSpace(operationID) == "" {
		return nil, false, nil
	}
	operationHash := directImportOperationHash(projectID, sourceProfile, operationID)
	if key, err := s.directImportDigestKey(); err != nil {
		return nil, false, err
	} else if len(key) > 0 {
		operationHash, err = directImportHMAC(key, directImportOperationDigestDomain, []string{
			normalizeProjectID(projectID),
			strings.TrimSpace(sourceProfile),
			strings.TrimSpace(operationID),
		})
		if err != nil {
			return nil, false, err
		}
	}
	ownerToken := uuid.NewString()
	claim, err := store.ClaimDirectImportReceipt(
		projectID,
		sourceProfile,
		operationHash,
		requestSHA256,
		ownerToken,
		time.Now().UTC(),
		directImportReceiptLease(),
	)
	if err != nil {
		return nil, false, err
	}
	return &directImportAttempt{
		store: store, receipt: claim.Receipt, ownerToken: ownerToken, created: claim.Created,
	}, claim.Replay, nil
}

func (attempt *directImportAttempt) operationHash() string {
	if attempt == nil || attempt.receipt == nil {
		return ""
	}
	return attempt.receipt.OperationKeyHash
}

func stampJMeterDirectImportOperation(result *jmeter.ConvertResult, operationID string) {
	if result == nil || strings.TrimSpace(operationID) == "" {
		return
	}
	for _, script := range result.Scripts {
		if script == nil {
			continue
		}
		if script.Metadata == nil {
			script.Metadata = &dsl.ScriptMetadata{}
		}
		if script.Metadata.Import == nil {
			script.Metadata.Import = &dsl.ImportMetadata{Source: "jmeter"}
		}
		if script.Metadata.Import.Preserved == nil {
			script.Metadata.Import.Preserved = make(map[string]any)
		}
		script.Metadata.Import.Preserved[jmeterDirectImportOperationKey] = operationID
	}
}

func jmeterDirectImportOperationID(scripts []dsl.Script) (string, error) {
	operationID := ""
	found := 0
	for index := range scripts {
		script := &scripts[index]
		if script.Metadata == nil || script.Metadata.Import == nil ||
			script.Metadata.Import.Preserved == nil {
			continue
		}
		value, _ := script.Metadata.Import.Preserved[jmeterDirectImportOperationKey].(string)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		found++
		if operationID == "" {
			operationID = value
		} else if operationID != value {
			return "", errors.New("JMeter scripts contain conflicting import operation ids")
		}
	}
	if found > 0 && found != len(scripts) {
		return "", errors.New("JMeter import operation id is missing from one or more scripts")
	}
	return operationID, nil
}

func directJMeterImportRequestDigest(
	s *Server,
	scripts []*dsl.Script,
	scenario *jmeter.ScenarioDraft,
) (string, error) {
	return s.directImportScriptsRequestDigest(scripts, scenario)
}

// directImportScriptsRequestDigest fingerprints the exact executable request,
// including imported credentials. The durable value is a keyed HMAC so a
// receipt-table reader cannot use it as an offline credential oracle. In
// lightweight configurations without durable receipts the digest is never
// stored, so a plain exact hash is sufficient.
func (s *Server) directImportScriptsRequestDigest(
	scripts []*dsl.Script,
	supplemental any,
) (string, error) {
	exact := make([]*dsl.Script, 0, len(scripts))
	for _, script := range scripts {
		copy := normalizedDirectImportScript(script)
		if copy == nil {
			exact = append(exact, nil)
			continue
		}
		exact = append(exact, copy)
	}
	payload := struct {
		Scripts      []*dsl.Script
		Supplemental any
	}{
		Scripts:      exact,
		Supplemental: supplemental,
	}
	key, err := s.directImportDigestKey()
	if err != nil {
		return "", err
	}
	if len(key) == 0 {
		return hashCanonical(payload), nil
	}
	return directImportHMAC(key, directImportRequestDigestDomain, payload)
}

func (s *Server) directImportExactScriptDigest(script *dsl.Script) (string, error) {
	exact := normalizedDirectImportScript(script)
	key, err := s.directImportDigestKey()
	if err != nil {
		return "", err
	}
	if len(key) == 0 {
		return hashCanonical(exact), nil
	}
	return directImportHMAC(key, directImportScriptDigestDomain, exact)
}

func normalizedDirectImportScript(script *dsl.Script) *dsl.Script {
	copy := cloneScriptDefinition(script)
	if copy == nil {
		return nil
	}
	copy.ID = ""
	copy.RefKey = ""
	copy.CreatedAt = time.Time{}
	copy.UpdatedAt = time.Time{}
	return copy
}

func (s *Server) directImportDigestKey() ([]byte, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	if _, ok := s.store.(storage.DirectImportReceiptStore); !ok {
		return nil, nil
	}
	raw, err := s.store.GetSetting(directImportDigestKeySetting)
	if err != nil {
		return nil, fmt.Errorf("load direct import digest key: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		generated := make([]byte, directImportDigestKeyBytes)
		if _, err := rand.Read(generated); err != nil {
			return nil, fmt.Errorf("generate direct import digest key: %w", err)
		}
		encoded := base64.RawURLEncoding.EncodeToString(generated)
		cas, ok := s.store.(settingsCompareAndSwapper)
		if !ok {
			return nil, errors.New("durable direct import receipts require atomic digest-key initialization")
		}
		won, err := cas.CompareAndSwapSetting(directImportDigestKeySetting, "", encoded)
		if err != nil {
			return nil, fmt.Errorf("initialize direct import digest key: %w", err)
		}
		if won {
			return generated, nil
		}
		raw, err = s.store.GetSetting(directImportDigestKeySetting)
		if err != nil {
			return nil, fmt.Errorf("reload direct import digest key: %w", err)
		}
	}
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(key) != directImportDigestKeyBytes {
		return nil, errors.New("direct import digest key is invalid")
	}
	return key, nil
}

func directImportHMAC(key []byte, domain string, value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode direct import digest payload: %w", err)
	}
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte(domain))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(payload)
	return "hmac-sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func (attempt *directImportAttempt) complete(
	storedScripts []*StoredScript,
	storedScenario *StoredScenario,
) error {
	if attempt == nil {
		return nil
	}
	scriptIDs := make([]string, 0, len(storedScripts))
	for _, script := range storedScripts {
		if script == nil || strings.TrimSpace(script.ID) == "" {
			return errors.New("direct import completion contains an empty stored script")
		}
		scriptIDs = append(scriptIDs, script.ID)
	}
	scenarioID := ""
	if storedScenario != nil {
		scenarioID = storedScenario.ID
	}
	completed, err := attempt.store.CompleteDirectImportReceipt(
		attempt.receipt.ID,
		attempt.ownerToken,
		scriptIDs,
		scenarioID,
		time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	if !completed {
		return errors.New("direct import receipt ownership was lost before completion")
	}
	return nil
}

func (attempt *directImportAttempt) renew() error {
	if attempt == nil {
		return nil
	}
	owned, err := attempt.store.RenewDirectImportReceipt(
		attempt.receipt.ID,
		attempt.ownerToken,
		time.Now().UTC(),
		directImportReceiptLease(),
	)
	if err != nil {
		return err
	}
	if !owned {
		return errors.New("direct import receipt ownership was lost")
	}
	return nil
}

// rollback owns the receipt fence before compensating deterministic assets.
// If ownership has moved to a stale-retry healer, this request must not delete
// anything the newer owner may already have adopted.
func (attempt *directImportAttempt) rollback(transaction *createImportTransaction) error {
	if attempt == nil {
		if transaction == nil {
			return nil
		}
		return transaction.rollback()
	}
	owned, err := attempt.store.RenewDirectImportReceipt(
		attempt.receipt.ID,
		attempt.ownerToken,
		time.Now().UTC(),
		directImportReceiptLease(),
	)
	if err != nil {
		return err
	}
	if !owned {
		return errors.New("direct import receipt ownership moved; compensation skipped")
	}
	if transaction != nil {
		if err := transaction.rollback(); err != nil {
			return err
		}
	}
	abandoned, err := attempt.store.AbandonDirectImportReceipt(
		attempt.receipt.ID,
		attempt.ownerToken,
	)
	if err != nil {
		return err
	}
	if !abandoned {
		return errors.New("direct import receipt was not abandoned by its current owner")
	}
	return nil
}

func (s *Server) loadDirectImportReceiptAssets(
	attempt *directImportAttempt,
) ([]*StoredScript, *StoredScenario, error) {
	if attempt == nil || attempt.receipt == nil {
		return nil, nil, errors.New("direct import receipt is required")
	}
	if len(attempt.receipt.ScriptIDs) == 0 {
		return nil, nil, errors.New("completed direct import receipt has no script ids")
	}
	return s.loadDirectImportAssetsByIDs(
		attempt.receipt.ProjectID,
		attempt.receipt.ScriptIDs,
		attempt.receipt.ScenarioID,
	)
}

func (s *Server) loadDirectImportAssetsByIDs(
	projectID string,
	scriptIDs []string,
	scenarioID string,
) ([]*StoredScript, *StoredScenario, error) {
	if len(scriptIDs) == 0 {
		return nil, nil, errors.New("direct import has no script ids")
	}
	projectID = normalizeProjectID(projectID)
	scripts := make([]*StoredScript, 0, len(scriptIDs))
	for _, id := range scriptIDs {
		stored, ok, err := s.runner.GetScriptDurable(id)
		if err != nil {
			return nil, nil, fmt.Errorf("load direct import script %s: %w", id, err)
		}
		if !ok || stored == nil {
			return nil, nil, fmt.Errorf("direct import script %s is missing", id)
		}
		if normalizeProjectID(stored.ProjectID) != projectID {
			return nil, nil, fmt.Errorf("direct import script %s belongs to a different project", id)
		}
		scripts = append(scripts, stored)
	}
	var scenario *StoredScenario
	if id := strings.TrimSpace(scenarioID); id != "" {
		var ok bool
		scenario, ok = s.runner.GetScenario(id)
		if !ok || scenario == nil {
			return nil, nil, fmt.Errorf("direct import scenario %s is missing", id)
		}
		if normalizeProjectID(scenario.ProjectID) != projectID {
			return nil, nil, fmt.Errorf("direct import scenario %s belongs to a different project", id)
		}
	}
	return scripts, scenario, nil
}

func deterministicScriptIDs(scripts []*dsl.Script) []string {
	ids := make([]string, 0, len(scripts))
	for _, script := range scripts {
		if script != nil {
			ids = append(ids, script.ID)
		}
	}
	return ids
}

func directImportPreparedAssetsMatch(
	s *Server,
	storedScripts []*StoredScript,
	preparedScripts []*dsl.Script,
	storedScenario *StoredScenario,
	scenarioRequest *CreateScenarioRequest,
) bool {
	if len(storedScripts) != len(preparedScripts) {
		return false
	}
	for index := range preparedScripts {
		if storedScripts[index] == nil || storedScripts[index].Script == nil ||
			!equivalentCreateOnlyScript(storedScripts[index].Script, preparedScripts[index]) {
			return false
		}
	}
	if scenarioRequest == nil {
		return storedScenario == nil
	}
	if storedScenario == nil {
		return false
	}
	return s.libraryScenarioRequestRecoveryHash(
		createScenarioRequestFromStored(storedScenario),
	) == s.libraryScenarioRequestRecoveryHash(*scenarioRequest)
}
