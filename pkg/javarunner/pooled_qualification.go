package javarunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const PooledQualificationSchema = "apexload.java-vuser-pilot-qualification/v1"

var ErrPooledQualification = errors.New("pooled_qualification_invalid")
var ErrPooledQualificationChanged = errors.New("pooled_qualification_changed")

// PooledResourceProfile is a total host reservation, not just a Java heap.
// Enforcement and shared fleet quotas are separate runner responsibilities.
type PooledResourceProfile struct {
	ID                                                                                           string
	SlotsPerHost                                                                                 int
	HeapBytes, NativeBytes, StackBytes, MetaspaceBytes, CodeCacheBytes, CaptureBytes, OtherBytes int64
}

func (p PooledResourceProfile) memoryBytes() (int64, bool) {
	if !qualificationName.MatchString(p.ID) || p.SlotsPerHost < 2 || p.SlotsPerHost > MaxPooledSlotsPerHost || p.OtherBytes < 0 || p.CaptureBytes < 576<<10 {
		return 0, false
	}
	total := p.OtherBytes
	for _, n := range []int64{p.HeapBytes, p.NativeBytes, p.StackBytes, p.MetaspaceBytes, p.CodeCacheBytes, p.CaptureBytes} {
		if n <= 0 || n > math.MaxInt64-total {
			return 0, false
		}
		total += n
	}
	return total, true
}

// PooledQualificationRequest is an internal, already-resolved workload. It is
// never decoded from a script. All paths must refer to operator-controlled
// immutable assets; hashing cannot prevent a writer changing files after a check.
// SourceArchive identifies the original source bundle as well as executable jars.
type PooledQualificationRequest struct {
	Launch         PooledLaunchSpec
	SourceArchive  string
	SharingProfile string
	Resources      PooledResourceProfile
}

// PooledQualificationEvidence records operator-reviewed evidence, not an
// automated proof of isolation. Inspection includes source/bytecode/libraries;
// representative runs and loader/cleanup checks are independently required.
// Unknown native use and unapproved cleanup remain dedicated.
type PooledQualificationEvidence struct {
	ApprovedBy                                                                         string
	InspectionSHA256, RepresentativeRunSHA256, LoaderChecksSHA256, CleanupChecksSHA256 string
	NativeAssessment                                                                   string
}

type PooledQualificationProfile struct {
	Schema, ID, ConfigurationSHA256 string
	Evidence                        PooledQualificationEvidence
}

type PooledQualificationRegistry struct {
	profiles map[string]PooledQualificationProfile
}

var qualificationName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// NewPooledQualificationRegistry accepts trusted operator configuration only.
// Presence in this registry permits a limited pilot, never production qualification.
func NewPooledQualificationRegistry(profiles []PooledQualificationProfile) (*PooledQualificationRegistry, error) {
	if len(profiles) > 1024 {
		return nil, ErrPooledQualification
	}
	r := &PooledQualificationRegistry{profiles: make(map[string]PooledQualificationProfile, len(profiles))}
	for _, p := range profiles {
		e := p.Evidence
		if p.Schema != PooledQualificationSchema || !qualificationName.MatchString(p.ID) || !canonicalDigest(p.ConfigurationSHA256) || !qualificationName.MatchString(e.ApprovedBy) || e.NativeAssessment != "no_application_native_observed" ||
			!canonicalDigest(e.InspectionSHA256) || !canonicalDigest(e.RepresentativeRunSHA256) || !canonicalDigest(e.LoaderChecksSHA256) || !canonicalDigest(e.CleanupChecksSHA256) {
			return nil, ErrPooledQualification
		}
		if _, duplicate := r.profiles[p.ID]; duplicate {
			return nil, ErrPooledQualification
		}
		r.profiles[p.ID] = p
	}
	return r, nil
}

type PooledQualificationDecision struct {
	Mode, FallbackReason, QualificationProfile, ConfigurationSHA256, ResourceProfile, SharingProfile string
	ContainmentBackend                                                                               string
	TargetClass                                                                                      string
	SlotsPerHost                                                                                     int
	HostMemoryBytes                                                                                  int64
}

// PooledSelection owns private copies. Mutating a request or returned decision
// cannot change the selected mode or the launch's immutable identity.
type PooledSelection struct {
	decision PooledQualificationDecision
	request  PooledQualificationRequest
}

func (s PooledSelection) Decision() PooledQualificationDecision { return s.decision }

// Select freezes a pre-reservation decision. Invalid/unmatched profiles use
// dedicated execution without changing its existing option semantics. Cancellation
// is an error rather than a compatibility fallback. No JVM or script is started.
func (r *PooledQualificationRegistry) Select(ctx context.Context, profile string, slotsPerHost int, request PooledQualificationRequest) (PooledSelection, error) {
	s := PooledSelection{decision: PooledQualificationDecision{Mode: "dedicated", SlotsPerHost: 1}}
	fallback := func(reason string) (PooledSelection, error) { s.decision.FallbackReason = reason; return s, nil }
	if err := ctx.Err(); err != nil {
		return s, err
	}
	if slotsPerHost == 1 {
		return fallback("dedicated_requested")
	}
	if slotsPerHost < 2 || slotsPerHost > MaxPooledSlotsPerHost {
		return fallback("pooled_capacity_invalid")
	}
	if !pooledRuntimeSupported(request.Launch.JDK) {
		return fallback("pooled_runtime_unqualified")
	}
	if r == nil {
		return fallback("pooled_profile_missing")
	}
	p, ok := r.profiles[profile]
	if !ok {
		return fallback("pooled_profile_missing")
	}
	if slotsPerHost != request.Resources.SlotsPerHost {
		return fallback("pooled_resource_mismatch")
	}
	request = cloneQualificationRequest(request)
	digest, err := InspectPooledQualification(ctx, request)
	if ctx.Err() != nil {
		return s, ctx.Err()
	}
	if err != nil {
		return fallback("pooled_inputs_invalid")
	}
	if digest != p.ConfigurationSHA256 {
		return fallback("pooled_profile_mismatch")
	}
	total, _ := request.Resources.memoryBytes()
	request.Launch.ConfigurationSHA256 = digest
	s.request = request
	s.decision = PooledQualificationDecision{Mode: "pooled_pilot", TargetClass: request.Launch.TargetClass, QualificationProfile: p.ID, ConfigurationSHA256: digest, ResourceProfile: request.Resources.ID, SharingProfile: request.SharingProfile, ContainmentBackend: request.Launch.ContainmentBackend, SlotsPerHost: slotsPerHost, HostMemoryBytes: total}
	return s, nil
}

// LaunchProvider rechecks assets at provisioning. A changed input fails this
// reservation; it never switches to dedicated execution or replays initialization.
func (s PooledSelection) LaunchProvider() (PooledLaunchProvider, error) {
	if s.decision.Mode != "pooled_pilot" {
		return nil, ErrPooledQualification
	}
	return func(ctx context.Context, a HostAllocation) (PooledLaunchSpec, error) {
		if a.Binding.ConfigurationSHA256 != s.decision.ConfigurationSHA256 || a.MemoryBytes != s.decision.HostMemoryBytes || len(a.SlotIDs) < 1 || len(a.SlotIDs) > s.decision.SlotsPerHost {
			return PooledLaunchSpec{}, ErrHostIdentity
		}
		digest, err := InspectPooledQualification(ctx, s.request)
		if ctx.Err() != nil {
			return PooledLaunchSpec{}, ctx.Err()
		}
		if err != nil || digest != s.decision.ConfigurationSHA256 {
			return PooledLaunchSpec{}, ErrPooledQualificationChanged
		}
		return cloneQualificationRequest(s.request).Launch, nil
	}, nil
}

func cloneQualificationRequest(r PooledQualificationRequest) PooledQualificationRequest {
	r.Launch.ScriptClasspath = append([]string(nil), r.Launch.ScriptClasspath...)
	r.Launch.SharedClasspath = append([]string(nil), r.Launch.SharedClasspath...)
	r.Launch.VMParameters = append([]string(nil), r.Launch.VMParameters...)
	return r
}

// InspectPooledQualification calculates an identity for subsequent operator
// review; it does NOT approve it. Runtime hashing covers the entire JDK tree,
// including modules/native implementation libraries, not only java and release.
// Symlinks/special files are conservatively rejected. These scans cannot detect
// every application JNI use or prove that background work can be attributed.
func InspectPooledQualification(ctx context.Context, r PooledQualificationRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// JSON replaces malformed UTF-8. Reject it rather than allowing distinct
	// argv/runtime strings to collapse to the same approved configuration hash.
	values := append([]string{r.Launch.JDK.Version, string(r.Launch.JDK.Source)}, r.Launch.VMParameters...)
	for _, value := range values {
		if !utf8.ValidString(value) {
			return "", ErrPooledQualification
		}
	}
	total, ok := r.Resources.memoryBytes()
	if !ok || !qualificationName.MatchString(r.SharingProfile) || r.Launch.ContainmentBackend != "delegated_linux" || !r.Launch.QualifiedCleanup {
		return "", ErrPooledQualification
	}
	heap, ok := pooledMaxHeap(r.Launch.VMParameters)
	if !ok || heap != uint64(r.Resources.HeapBytes) {
		return "", ErrPooledQualification
	}
	// Reuse the actual launcher policy without executing a process. The supplied
	// configuration digest is ignored: only verified contents define the identity.
	r = cloneQualificationRequest(r)
	r.Launch.ConfigurationSHA256 = strings.Repeat("0", 64)
	a := HostAllocation{ID: "qualification", SlotIDs: []string{"slot"}, MemoryBytes: total, Binding: HostBinding{ConfigurationSHA256: r.Launch.ConfigurationSHA256}}
	if _, err := buildPooledCommand(r.Launch, a, 1, pooledRuntimeSupported); err != nil {
		return "", ErrPooledQualification
	}
	if !canonicalQualificationPath(r.Launch.WorkDir) || !canonicalQualificationPath(r.Launch.JDK.Home) || !canonicalQualificationPath(r.Launch.JDK.JavaBin) {
		return "", ErrPooledQualification
	}
	rel, err := filepath.Rel(r.Launch.JDK.Home, r.Launch.JDK.JavaBin)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrPooledQualification
	}
	for _, path := range []string{r.Launch.JDK.JavaBin, filepath.Join(r.Launch.JDK.Home, "release")} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", ErrPooledQualification
		}
	}
	scan := qualificationScanner{ctx: ctx}
	runtimeFiles := []qualificationFile{}
	err = filepath.WalkDir(r.Launch.JDK.Home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ErrPooledQualification
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		scan.entries++
		if scan.entries > 32768 {
			return ErrPooledQualification
		}
		if entry.IsDir() {
			return nil
		}
		f, err := scan.file(path)
		if err != nil {
			return err
		}
		f.Path, err = filepath.Rel(r.Launch.JDK.Home, path)
		if err != nil {
			return ErrPooledQualification
		}
		f.Path = filepath.ToSlash(f.Path)
		runtimeFiles = append(runtimeFiles, f)
		return nil
	})
	if err != nil {
		return "", qualificationError(ctx)
	}
	artifacts := []qualificationFile{}
	paths := []string{r.Launch.HarnessJar, r.Launch.ShimJar, r.SourceArchive}
	paths = append(paths, r.Launch.ScriptClasspath...)
	paths = append(paths, r.Launch.SharedClasspath...)
	for i, path := range paths {
		f, err := scan.file(path)
		if err != nil {
			return "", qualificationError(ctx)
		}
		// The source archive is an identity input, not a JVM classpath entry.
		if i != 2 {
			if err := validateQualificationJar(ctx, path); err != nil {
				return "", qualificationError(ctx)
			}
		}
		artifacts = append(artifacts, f)
	}
	// Struct/slice encoding gives deterministic ordering; the private/shared split
	// and every exact ordered VM token remain part of the configuration identity.
	identity := struct {
		Schema             string
		Request            PooledQualificationRequest
		Runtime, Artifacts []qualificationFile
	}{PooledQualificationSchema, r, runtimeFiles, artifacts}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", ErrPooledQualification
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}
func canonicalQualificationPath(path string) bool {
	if !utf8.ValidString(path) || !filepath.IsAbs(path) || path != strings.TrimSpace(path) || path != filepath.Clean(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && resolved == path
}
func qualificationError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrPooledQualification
}

type qualificationFile struct {
	Path, SHA256 string
	Size         int64
	Mode         uint32
}
type qualificationScanner struct {
	ctx     context.Context
	bytes   int64
	entries int
}

func (s *qualificationScanner) file(path string) (qualificationFile, error) {
	bad := func() (qualificationFile, error) { return qualificationFile{}, qualificationError(s.ctx) }
	if s.ctx.Err() != nil || !canonicalQualificationPath(path) {
		return bad()
	}
	initial, err := os.Lstat(path)
	if err != nil || !initial.Mode().IsRegular() || initial.Size() < 0 || initial.Size() > 2<<30 || initial.Size() > (4<<30)-s.bytes {
		return bad()
	}
	f, err := os.Open(path)
	if err != nil {
		return bad()
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !os.SameFile(initial, before) || !before.Mode().IsRegular() || before.Size() != initial.Size() {
		return bad()
	}
	h := sha256.New()
	reader := &qualificationReader{ctx: s.ctx, reader: io.LimitReader(f, initial.Size()+1)}
	n, err := io.Copy(h, reader)
	after, statErr := f.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || statErr != nil || pathErr != nil || n != initial.Size() || !os.SameFile(before, current) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		return bad()
	}
	s.bytes += n
	return qualificationFile{Path: path, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n, Mode: uint32(before.Mode().Perm())}, nil
}

type qualificationReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *qualificationReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
