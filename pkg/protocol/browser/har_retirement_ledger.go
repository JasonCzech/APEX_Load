package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const harRetirementLedgerName = "retirements.json"
const maxHARRetirementLedgerBytes = 64 << 10
const maxHARRetirementRecords = 128

type harPersistentIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    uint32 `json:"uid"`
}
type harRetirementRecord struct {
	Kind       string                `json:"kind"`
	Generation string                `json:"generation"`
	Name       string                `json:"name,omitempty"`
	Identity   harPersistentIdentity `json:"identity"`
}

// harRetirementPlanCursor is opaque process-local authorization for one
// bounded plan. It advances only after this batch's own verified durable
// mutation and descriptor close; an external replacement is never adopted.
type harRetirementPlanCursor struct {
	mu       sync.Mutex
	identity os.FileInfo
	records  []harRetirementRecord
}

func equalHARRetirementRecords(a, b []harRetirementRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (c *harRetirementPlanCursor) afterMutation(work *os.Root, removed *harRetirementRecord) (os.FileInfo, []harRetirementRecord, error) {
	ledger, info, err := readHARRetirementLedger(work)
	if err != nil || info == nil {
		return nil, nil, errors.Join(err, errHAROwnershipUnproven)
	}
	expected := make([]harRetirementRecord, 0, len(c.records))
	found := removed == nil
	for _, r := range c.records {
		if removed != nil && r == *removed {
			found = true
		} else {
			expected = append(expected, r)
		}
	}
	if !found || !equalHARRetirementRecords(expected, ledger.Records) {
		return nil, nil, errHAROwnershipUnproven
	}
	return info, expected, nil
}

type harRetirementLedger struct {
	Version int                   `json:"version"`
	Records []harRetirementRecord `json:"records"`
}

func (r harRetirementRecord) key() string { return r.Kind + "/" + r.Generation }
func (r harRetirementRecord) names() (string, string) {
	if r.Kind == "working" {
		return r.Generation, ".retired-" + r.Generation
	}
	return r.Name, ".har-retired-" + r.Generation
}
func validHARLedgerRecord(r harRetirementRecord) bool {
	if !validHARGeneration(r.Generation) || r.Identity.Inode == 0 || r.Identity.UID != uint32(os.Geteuid()) {
		return false
	}
	if r.Kind == "working" {
		return r.Name == ""
	}
	for _, c := range r.Name {
		if c < 33 || c > 126 {
			return false
		}
	}
	return r.Kind == "completed" && len(r.Name) <= 256 && r.Name == filepath.Base(r.Name) && !strings.ContainsAny(r.Name, "/\\") && isRetainedRuntimeHARArtifact(r.Name)
}

// strictHARJSON rejects malformed UTF-8, duplicate decoded keys, excess nesting
// and trailing values before decoding an immutable ownership contract.
func strictHARJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errHAROwnershipUnproven
	}
	d := json.NewDecoder(bytes.NewReader(data))
	tokens := 0
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 8 {
			return errHAROwnershipUnproven
		}
		tokens++
		if tokens > 4096 {
			return errHAROwnershipUnproven
		}
		t, err := d.Token()
		if err != nil {
			return errHAROwnershipUnproven
		}
		switch v := t.(type) {
		case json.Delim:
			switch v {
			case '{':
				keys := map[string]bool{}
				for d.More() {
					k, err := d.Token()
					key, ok := k.(string)
					if err != nil || !ok || keys[key] {
						return errHAROwnershipUnproven
					}
					keys[key] = true
					if err := visit(depth + 1); err != nil {
						return err
					}
				}
				t, err = d.Token()
				if err != nil || t != json.Delim('}') {
					return errHAROwnershipUnproven
				}
			case '[':
				for d.More() {
					if err := visit(depth + 1); err != nil {
						return err
					}
				}
				t, err = d.Token()
				if err != nil || t != json.Delim(']') {
					return errHAROwnershipUnproven
				}
			default:
				return errHAROwnershipUnproven
			}
		}
		return nil
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errHAROwnershipUnproven
	}
	if err := strictHARFields(data, target); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errHAROwnershipUnproven
	}
	return nil
}

func strictHARFields(data []byte, target any) error {
	check := func(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return nil, errHAROwnershipUnproven
		}
		allowed := map[string]bool{}
		for _, key := range required {
			allowed[key] = true
			if _, ok := object[key]; !ok {
				return nil, errHAROwnershipUnproven
			}
		}
		for _, key := range optional {
			allowed[key] = true
		}
		for key, raw := range object {
			if !allowed[key] {
				return nil, errHAROwnershipUnproven
			}
			value := bytes.TrimSpace(raw)
			if len(value) == 0 {
				return nil, errHAROwnershipUnproven
			}
			// encoding/json accepts null for integer and string destinations.
			// Ownership contracts require actual typed scalars, including UID 0.
			switch key {
			case "version", "device", "inode", "uid", "pid":
				if value[0] < '0' || value[0] > '9' {
					return nil, errHAROwnershipUnproven
				}
			case "kind", "generation", "state", "name", "role", "machine", "boot", "namespace", "start":
				if value[0] != '"' {
					return nil, errHAROwnershipUnproven
				}
			case "records", "processes":
				if value[0] != '[' {
					return nil, errHAROwnershipUnproven
				}
			case "identity":
				if value[0] != '{' {
					return nil, errHAROwnershipUnproven
				}
			}
		}
		return object, nil
	}
	switch target.(type) {
	case *harOwnershipManifest:
		object, err := check(data, []string{"version", "generation", "state"}, []string{"processes"})
		if err != nil {
			return err
		}
		if raw, ok := object["processes"]; ok {
			var processes []json.RawMessage
			if json.Unmarshal(raw, &processes) != nil || processes == nil || len(processes) > 2 {
				return errHAROwnershipUnproven
			}
			for _, process := range processes {
				if _, err := check(process, []string{"role", "boot", "namespace", "pid", "start"}, []string{"machine"}); err != nil {
					return err
				}
			}
		}
		return nil
	case *harRetirementLedger:
		object, err := check(data, []string{"version", "records"}, nil)
		if err != nil {
			return err
		}
		var records []json.RawMessage
		if json.Unmarshal(object["records"], &records) != nil || records == nil || len(records) > maxHARRetirementRecords {
			return errHAROwnershipUnproven
		}
		for _, raw := range records {
			record, err := check(raw, []string{"kind", "generation", "identity"}, []string{"name"})
			if err != nil {
				return err
			}
			if _, err := check(record["identity"], []string{"device", "inode", "uid"}, nil); err != nil {
				return err
			}
		}
		return nil
	default:
		return errHAROwnershipUnproven
	}
}

func readHARRetirementLedger(work *os.Root) (harRetirementLedger, os.FileInfo, error) {
	ledger := harRetirementLedger{Version: 1, Records: []harRetirementRecord{}}
	if _, err := work.Lstat(harRetirementLedgerName); os.IsNotExist(err) {
		return ledger, nil, nil
	} else if err != nil {
		return ledger, nil, err
	}
	f, err := openHARRegular(work, harRetirementLedgerName, false)
	if err != nil {
		return ledger, nil, err
	}
	info, statErr := f.Stat()
	data, readErr := io.ReadAll(io.LimitReader(f, maxHARRetirementLedgerBytes+1))
	err = errors.Join(statErr, readErr, f.Close())
	if err != nil || len(data) > maxHARRetirementLedgerBytes {
		return ledger, nil, errors.Join(err, errHAROwnershipUnproven)
	}
	if err := strictHARJSON(data, &ledger); err != nil {
		return ledger, nil, err
	}
	if ledger.Version != 1 || ledger.Records == nil || len(ledger.Records) > maxHARRetirementRecords {
		return ledger, nil, errHAROwnershipUnproven
	}
	keys := map[string]bool{}
	for _, r := range ledger.Records {
		if !validHARLedgerRecord(r) || keys[r.key()] {
			return ledger, nil, errHAROwnershipUnproven
		}
		keys[r.key()] = true
	}
	return ledger, info, nil
}

func writeHARRetirementLedger(work *os.Root, ledger harRetirementLedger, prior os.FileInfo) error {
	if ledger.Version != 1 || ledger.Records == nil || len(ledger.Records) > maxHARRetirementRecords {
		return errHAROwnershipUnproven
	}
	data, err := json.Marshal(ledger)
	if err != nil || len(data) > maxHARRetirementLedgerBytes {
		return errHAROwnershipUnproven
	}
	// Verify the exact prior descriptor identity while the permanent namespace
	// lease is held; malformed/replaced state never becomes an empty ledger.
	current, err := work.Lstat(harRetirementLedgerName)
	if prior == nil {
		if !os.IsNotExist(err) {
			return errHAROwnershipUnproven
		}
	} else if err != nil || !os.SameFile(prior, current) {
		return errHAROwnershipUnproven
	}
	gen, err := harRandomGeneration()
	if err != nil {
		return err
	}
	temp := ".retirement-" + gen + ".tmp"
	if err := writeHARPrivateDurable(work, temp, data); err != nil {
		return err
	}
	if err := syncHARRoot(work); err != nil {
		return err
	}
	current, err = work.Lstat(harRetirementLedgerName)
	if prior == nil {
		if !os.IsNotExist(err) {
			return errHAROwnershipUnproven
		}
	} else if err != nil || !os.SameFile(prior, current) {
		return errHAROwnershipUnproven
	}
	if err := work.Rename(temp, harRetirementLedgerName); err != nil {
		return err
	}
	return syncHARRoot(work)
}

func registerHARRetirement(work *os.Root, r harRetirementRecord) error {
	if !validHARLedgerRecord(r) {
		return errHAROwnershipUnproven
	}
	ledger, info, err := readHARRetirementLedger(work)
	if err != nil {
		return err
	}
	for _, old := range ledger.Records {
		if old.key() == r.key() {
			if old != r {
				return errHAROwnershipUnproven
			}
			return nil
		}
	}
	if len(ledger.Records) >= maxHARRetirementRecords {
		return fmt.Errorf("HAR retirement record limit reached")
	}
	ledger.Records = append(ledger.Records, r)
	return writeHARRetirementLedger(work, ledger, info)
}
func clearHARRetirement(work *os.Root, r harRetirementRecord) error {
	ledger, info, err := readHARRetirementLedger(work)
	if err != nil {
		return err
	}
	found := false
	records := make([]harRetirementRecord, 0, len(ledger.Records))
	for _, old := range ledger.Records {
		if old.key() == r.key() {
			if old != r {
				return errHAROwnershipUnproven
			}
			found = true
		} else {
			records = append(records, old)
		}
	}
	if !found {
		return errHAROwnershipUnproven
	}
	ledger.Records = records
	return writeHARRetirementLedger(work, ledger, info)
}
func findHARRetirement(work *os.Root, kind, generation string) (harRetirementRecord, bool, error) {
	ledger, _, err := readHARRetirementLedger(work)
	if err != nil {
		return harRetirementRecord{}, false, err
	}
	for _, r := range ledger.Records {
		if r.Kind == kind && r.Generation == generation {
			return r, true, nil
		}
	}
	return harRetirementRecord{}, false, nil
}

// harRetirementPayload chooses only one exact recorded original/tombstone.
// Seeing a replacement, two payloads, or a wrong type preserves both names.
func harRetirementPayload(root *os.Root, r harRetirementRecord) (string, os.FileInfo, error) {
	source, tomb := r.names()
	a, aerr := root.Lstat(source)
	b, berr := root.Lstat(tomb)
	if aerr != nil && !os.IsNotExist(aerr) {
		return "", nil, aerr
	}
	if berr != nil && !os.IsNotExist(berr) {
		return "", nil, berr
	}
	if aerr == nil && berr == nil {
		return "", nil, errHAROwnershipUnproven
	}
	name, info := source, a
	if aerr != nil {
		name, info = tomb, b
	}
	if info == nil {
		return tomb, nil, nil
	}
	actual, err := persistentHARIdentity(info)
	if err != nil || actual != r.Identity || info.Mode()&os.ModeSymlink != 0 || (r.Kind == "working" && !info.IsDir()) || (r.Kind == "completed" && !info.Mode().IsRegular()) {
		return "", nil, errHAROwnershipUnproven
	}
	return name, info, nil
}

func isHARLedgerAuxiliary(name string) bool {
	return name == harRetirementLedgerName || strings.HasPrefix(name, ".retirement-") && strings.HasSuffix(name, ".tmp") && validHARGeneration(strings.TrimSuffix(strings.TrimPrefix(name, ".retirement-"), ".tmp"))
}

// RemoveRuntimeHARArtifactAt retires an exact completed scrubbed artifact.
// Callers hold WithRuntimeHARArtifactNamespace; raw payloads use join proofs.
func RemoveRuntimeHARArtifactAt(parent *os.Root, name string, identity os.FileInfo) error {
	if identity == nil || !isRetainedRuntimeHARArtifact(name) || name != filepath.Base(name) {
		return errHAROwnershipUnproven
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(identity, info) || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errHAROwnershipUnproven
	}
	work, err := openHARPrivateChild(parent, runtimeHARWorkingDirectory, false)
	if err != nil {
		return err
	}
	defer work.Close()
	id, err := persistentHARIdentity(info)
	if err != nil {
		return err
	}
	gen, err := harRandomGeneration()
	if err != nil {
		return err
	}
	r := harRetirementRecord{Kind: "completed", Generation: gen, Name: name, Identity: id}
	if err := registerHARRetirement(work, r); err != nil {
		return err
	}
	return resumeHARCompletedRetirement(parent, work, r)
}
func resumeHARCompletedRetirement(parent, work *os.Root, r harRetirementRecord) error {
	name, info, err := harRetirementPayload(parent, r)
	if err != nil {
		return err
	}
	_, tomb := r.names()
	if info != nil {
		if name != tomb {
			if err := parent.Rename(name, tomb); err != nil {
				return err
			}
			if err := syncHARRoot(parent); err != nil {
				return err
			}
		}
		actual, err := parent.Lstat(tomb)
		if err != nil {
			return err
		}
		id, err := persistentHARIdentity(actual)
		if err != nil || id != r.Identity {
			return errHAROwnershipUnproven
		}
		if err := parent.Remove(tomb); err != nil {
			return err
		}
	}
	if err := syncHARRoot(parent); err != nil {
		return err
	}
	// Recheck both names before clearing proof, even after a missing payload.
	if _, info, err := harRetirementPayload(parent, r); err != nil || info != nil {
		return errors.Join(err, errHAROwnershipUnproven)
	}
	return clearHARRetirement(work, r)
}

func checkHARRetirementAdmission(work *os.Root) error {
	ledger, _, err := readHARRetirementLedger(work)
	if err != nil {
		return fmt.Errorf("HAR retirement state unavailable: %w", err)
	}
	if len(ledger.Records) >= maxHARRetirementRecords {
		return fmt.Errorf("HAR retirement record limit reached")
	}
	return nil
}

func readHARLedgerCandidates(ctx context.Context, parent, work *os.Root, path string, limit int) ([]RuntimeHARWorkingPruneCandidate, *harRetirementPlanCursor, error) {
	ledger, ledgerInfo, err := readHARRetirementLedger(work)
	if err != nil {
		return nil, nil, err
	}
	parentInfo, err := parent.Stat(".")
	if err != nil {
		return nil, nil, err
	}
	out := make([]RuntimeHARWorkingPruneCandidate, 0)
	cursor := &harRetirementPlanCursor{identity: ledgerInfo, records: append([]harRetirementRecord(nil), ledger.Records...)}
	for _, r := range ledger.Records {
		if err := ctx.Err(); err != nil {
			return out, cursor, err
		}
		if len(out) >= limit {
			break
		}
		root := work
		if r.Kind == "completed" {
			root = parent
		}
		name, info, err := harRetirementPayload(root, r)
		if err != nil {
			return out, cursor, err
		}
		var size int64
		modified := ledgerInfo.ModTime().UTC()
		if info != nil {
			modified = info.ModTime().UTC()
			if r.Kind == "working" {
				child, err := openHARPrivateChild(work, name, false)
				if err != nil {
					return out, cursor, err
				}
				size, err = harWorkingGenerationSize(child)
				closeErr := child.Close()
				if err != nil || closeErr != nil {
					return out, cursor, errors.Join(err, closeErr)
				}
			} else {
				size = info.Size()
			}
		}
		base := path
		if r.Kind == "working" {
			base = filepath.Join(path, runtimeHARWorkingDirectory)
		}
		copy := r
		out = append(out, RuntimeHARWorkingPruneCandidate{Path: filepath.Join(base, name), SizeBytes: size, ModifiedAt: modified, generation: r.Generation, directoryName: name, identity: info, parentIdentity: parentInfo, terminal: &copy, planCursor: cursor, Completed: r.Kind == "completed"})
	}
	return out, cursor, nil
}
