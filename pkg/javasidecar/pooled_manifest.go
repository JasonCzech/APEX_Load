package javasidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

const PooledPilotManifestSchema = "apexload.java-vuser-pilot-runner/v1"
const maxPooledPilotManifestBytes = 1 << 20

var ErrPooledPilotManifest = errors.New("pooled_pilot_manifest_invalid")

// PooledPilotManifest is operator configuration, never script or HTTP input.
// Nested qualification/request/budget records retain their existing Go field
// names and hashing format. Approval binds this exact DSL digest and runtime.
// Mount the manifest and assets under operator control; no hot reload is offered.
type PooledPilotManifest struct {
	Schema               string                                `json:"schema"`
	Qualification        javarunner.PooledQualificationProfile `json:"qualification"`
	Request              javarunner.PooledQualificationRequest `json:"request"`
	ScriptSHA256         string                                `json:"script_sha256"`
	Budget               javarunner.HostBudget                 `json:"budget"`
	DedicatedMemoryBytes int64                                 `json:"dedicated_memory_bytes"`
	AllowPreparation     bool                                  `json:"allow_preparation"`
	AllowCommit          bool                                  `json:"allow_commit"`
	AllowInvocation      bool                                  `json:"allow_invocation"`
}

// PooledPilotSettings owns a single-profile pilot and its shared capacity ledger.
// The same Admission must be used by dedicated and pooled paths in one server.
type PooledPilotSettings struct {
	Profiles     *PooledControlProfiles
	Admission    *javarunner.DedicatedHostAdmission
	Decision     PooledControlDecision
	ScriptSHA256 string
}

// LoadPooledPilotManifest performs cold, bounded inspection; it starts no JVM
// and cannot grant approval or convert incompatible inputs to another mode.
// maxProcesses is the existing operator process ceiling, never a new default.
func LoadPooledPilotManifest(ctx context.Context, path string, maxProcesses int) (*PooledPilotSettings, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !filepath.IsAbs(path) || path != strings.TrimSpace(path) {
		return nil, ErrPooledPilotManifest
	}
	initial, err := os.Stat(path)
	if err != nil || !initial.Mode().IsRegular() || initial.Size() > maxPooledPilotManifestBytes {
		return nil, ErrPooledPilotManifest
	}
	f, err := openPooledManifestFile(path)
	if err != nil {
		return nil, ErrPooledPilotManifest
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(initial, info) {
		return nil, ErrPooledPilotManifest
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPooledPilotManifestBytes+1))
	if err != nil || len(data) > maxPooledPilotManifestBytes || !utf8.Valid(data) || !uniquePilotJSONKeys(data) {
		return nil, ErrPooledPilotManifest
	}
	var m PooledPilotManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil {
		return nil, ErrPooledPilotManifest
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrPooledPilotManifest
	}
	return compilePooledPilotManifest(ctx, m, maxProcesses)
}

func compilePooledPilotManifest(ctx context.Context, m PooledPilotManifest, maxProcesses int) (*PooledPilotSettings, error) {
	if m.Schema != PooledPilotManifestSchema || !validPooledScriptSHA256(m.ScriptSHA256) || m.Budget.MaxHosts > maxProcesses || maxProcesses < 1 || m.DedicatedMemoryBytes < 1 || m.DedicatedMemoryBytes > m.Budget.MaxMemoryBytes {
		return nil, ErrPooledPilotManifest
	}
	registry, err := javarunner.NewPooledQualificationRegistry([]javarunner.PooledQualificationProfile{m.Qualification})
	if err != nil {
		return nil, ErrPooledPilotManifest
	}
	selection, err := registry.Select(ctx, m.Qualification.ID, m.Request.Resources.SlotsPerHost, m.Request)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || selection.Decision().Mode != "pooled_pilot" {
		return nil, ErrPooledPilotManifest
	}
	profile := PooledControlProfile{ScriptSHA256: m.ScriptSHA256, Selection: selection, Budget: m.Budget, AllowPreparation: m.AllowPreparation, AllowCommit: m.AllowCommit, AllowInvocation: m.AllowInvocation}
	profiles, err := NewPooledControlProfiles(map[string]PooledControlProfile{m.Qualification.ID: profile})
	if err != nil {
		return nil, ErrPooledPilotManifest
	}
	capacity, err := javarunner.NewHostCapacity(m.Budget)
	if err != nil {
		return nil, ErrPooledPilotManifest
	}
	admission, err := javarunner.NewDedicatedHostAdmission(capacity, m.DedicatedMemoryBytes)
	if err != nil {
		return nil, ErrPooledPilotManifest
	}
	d := selection.Decision()
	decision := PooledControlDecision{ScriptSHA256: m.ScriptSHA256, TargetClass: d.TargetClass, PreparationEnabled: m.AllowPreparation, CommitEnabled: m.AllowCommit, InvocationEnabled: m.AllowInvocation, Mode: d.Mode, ProfileID: m.Qualification.ID, ResourceProfile: d.ResourceProfile, ConfigurationSHA256: d.ConfigurationSHA256, ContainmentBackend: d.ContainmentBackend, SlotsPerHost: d.SlotsPerHost, HostMemoryBytes: d.HostMemoryBytes, MaxHosts: m.Budget.MaxHosts, MaxVUsers: m.Budget.MaxVUsers, MaxMemoryBytes: m.Budget.MaxMemoryBytes}
	return &PooledPilotSettings{Profiles: profiles, Admission: admission, Decision: decision, ScriptSHA256: m.ScriptSHA256}, nil
}

// encoding/json accepts repeated keys and case aliases. Reject both so operators
// and machines cannot read different approvals from the same bounded document.
func uniquePilotJSONKeys(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 32 {
			return false
		}
		tok, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				tok, err = d.Token()
				key, ok := tok.(string)
				if err != nil || !ok {
					return false
				}
				key = strings.Map(func(r rune) rune {
					lowest := r
					for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
						if next < lowest {
							lowest = next
						}
					}
					return lowest
				}, key)
				if keys[key] {
					return false
				}
				keys[key] = true
				if !value(depth + 1) {
					return false
				}
			}
			tok, err = d.Token()
			return err == nil && tok == json.Delim('}')
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			tok, err = d.Token()
			return err == nil && tok == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
