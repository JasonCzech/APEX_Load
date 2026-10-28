package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	collectionReceiptMaxBytes          int64 = 8 << 20
	collectionOptionalReceiptFileName        = "optional_receipt.json"
	maxCollectionRequiredFiles               = 5
	maxCollectionOptionalFiles               = protocol.MaxBrowserHARArtifactsPerRun + 1
	maxCollectionCacheDirectoryEntries       = 1 + maxCollectionRequiredFiles + 1 + maxCollectionOptionalFiles
)

var collectionPayloadNames = []string{"transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl"}

func collectionBrowserPayloadNames(m *RunArchiveManifest) []string {
	names := append([]string(nil), collectionPayloadNames...)
	if m.BrowserReceiptsFile != "" {
		names = append(names, browserReceiptsFileName)
	}
	if m.BrowserDocumentsFile != "" {
		names = append(names, browserDocumentsFileName)
	}
	return names
}

type collectionCacheStore interface {
	ConfigureRunArchiveCollectionCache(string, string, int64, string, string) (*storage.RunArchiveCollectionIntent, bool, error)
	SaveRunArchiveCollectionSource(string, string, string, storage.RunArchiveCollectionSource, string, string) (bool, error)
}
type collectionCachedFile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type collectionSourceReceipt struct {
	TestID    string                 `json:"test_id"`
	ProjectID string                 `json:"project_id"`
	StartTime time.Time              `json:"start_time"`
	Token     string                 `json:"token"`
	Target    string                 `json:"target"`
	Identity  string                 `json:"identity"`
	Manifest  json.RawMessage        `json:"manifest"`
	Files     []collectionCachedFile `json:"files"`
}
type collectionOptionalReceipt struct {
	TestID        string                 `json:"test_id"`
	Token         string                 `json:"token"`
	Target        string                 `json:"target"`
	Identity      string                 `json:"identity"`
	ReceiptSHA256 string                 `json:"receipt_sha256"`
	Files         []collectionCachedFile `json:"files"`
}
type collectionSourceCache struct {
	mu           sync.Mutex
	root         *os.Root
	intent       *storage.RunArchiveCollectionIntent
	store        collectionCacheStore
	tr           *TestRunner
	lease, owner string
	maxBytes     int64
}

func collectionCacheByteLimit(name string, fallback, max int64) (int64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 || n > max {
		return 0, fmt.Errorf("%s must be a positive byte count at most %d", name, max)
	}
	return n, nil
}

// Caller holds the per-run source lock for this handle's entire lifetime.
func (tr *TestRunner) openCollectionSourceCache(ctx context.Context, test *ManagedTest) (*collectionSourceCache, error) {
	if tr == nil {
		return nil, nil
	}
	store, ok := tr.testStore.(collectionCacheStore)
	if !ok {
		return nil, nil
	}
	intent, err := tr.runArchiveCollectionIntent(test)
	if err != nil || intent == nil {
		return nil, err
	}
	limit, err := collectionCacheByteLimit("APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES", 32<<30, 32<<30)
	if err != nil {
		return nil, err
	}
	max, err := collectionCacheByteLimit("APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES", 128<<30, 1<<50)
	if err != nil {
		return nil, err
	}
	lease, owner := "", ""
	if tr.durableExecutionFence {
		_, holder, _, err := tr.generatorExecutionClaim()
		if err != nil {
			return nil, err
		}
		lease, owner = executionLeaseName, holder
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	err = tr.trackDurableWrite(func() error {
		var err error
		intent, ok, err = store.ConfigureRunArchiveCollectionCache(test.ID, intent.IntentToken, limit, lease, owner)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("archive collection cache was fenced")
	}
	rootPath, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), true)
	if err != nil {
		return nil, err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	cacheRoot, err := openArchiveChildDirectoryAt(root, "_collection", true, 0o700)
	if err != nil {
		return nil, err
	}
	if err := syncOpenedArchiveDirectory(root); err != nil {
		_ = cacheRoot.Close()
		return nil, err
	}
	return &collectionSourceCache{root: cacheRoot, intent: intent, store: store, tr: tr, lease: lease, owner: owner, maxBytes: max}, nil
}
func (c *collectionSourceCache) close() { _ = c.root.Close() }
func (c *collectionSourceCache) targetRoot(target string, create bool) (*os.Root, error) {
	root := c.root
	var owned *os.Root
	for _, part := range []string{c.intent.TestID, c.intent.IntentToken, sha256BytesHex([]byte(target))} {
		next, err := openArchiveChildDirectoryAt(root, part, create, 0o700)
		if err == nil && create {
			if syncErr := syncOpenedArchiveDirectory(root); syncErr != nil {
				_ = next.Close()
				err = syncErr
			}
		}
		if owned != nil {
			_ = owned.Close()
		}
		if err != nil {
			return nil, err
		}
		owned = next
		root = next
	}
	return owned, nil
}
func (c *collectionSourceCache) load(ctx context.Context, target string) (*cachedArchiveSource, error) {
	// SQL anchors are shared across controllers, but staging may be per-pod.
	// Missing directories permit a refetch against the original immutable receipt.
	// An existing directory with missing/corrupt content remains fail-closed.
	c.mu.Lock()
	anchor, ok := c.intent.CachedSources[target]
	c.mu.Unlock()
	if !ok {
		return nil, nil
	}
	parent, err := c.targetRoot(target, false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	root, err := openArchiveChildDirectoryAt(parent, anchor.ReceiptSHA256, false, 0o700)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	source, err := c.decode(ctx, root, target, anchor)
	if err != nil {
		_ = root.Close()
	}
	return source, err
}
func (c *collectionSourceCache) decode(ctx context.Context, root *os.Root, target string, anchor storage.RunArchiveCollectionSource) (*cachedArchiveSource, error) {
	data, _, err := readArchiveFileAtRootLimitedContext(ctx, root, "receipt.json", collectionReceiptMaxBytes)
	if err != nil {
		return nil, err
	}
	if sha256BytesHex(data) != anchor.ReceiptSHA256 {
		return nil, errors.New("collection receipt hash mismatch")
	}
	var receipt collectionSourceReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, err
	}
	if receipt.TestID != c.intent.TestID || receipt.ProjectID != c.intent.ProjectID || !receipt.StartTime.Equal(c.intent.StartTime) || receipt.Token != c.intent.IntentToken || receipt.Target != target || receipt.Identity != c.intent.Sources[target] || (len(receipt.Files) < 3 || len(receipt.Files) > maxCollectionRequiredFiles) || int64(len(receipt.Manifest)) > remoteArchiveManifestMaxBytes {
		return nil, errors.New("collection receipt identity mismatch")
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(receipt.Manifest, &manifest); err != nil {
		return nil, err
	}
	if err := validateBrowserDocumentManifest(&manifest, true); err != nil {
		return nil, err
	}
	cacheNames := collectionBrowserPayloadNames(&manifest)
	if len(receipt.Files) != len(cacheNames) {
		return nil, errors.New("collection receipt browser artifact mismatch")
	}
	total := int64(len(data))
	for i, file := range receipt.Files {
		if file.Name != cacheNames[i] || file.Bytes < 0 || file.Bytes > c.intent.CacheLimitBytes-total || len(file.SHA256) != 64 {
			return nil, errors.New("invalid collection receipt payload")
		}
		total += file.Bytes
	}
	if total != anchor.Bytes || total > c.intent.CacheLimitBytes {
		return nil, errors.New("collection receipt size mismatch")
	}
	names := []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile}
	if manifest.BrowserReceiptsFile != "" {
		names = append(names, manifest.BrowserReceiptsFile)
	}
	if manifest.BrowserDocumentsFile != "" {
		names = append(names, manifest.BrowserDocumentsFile)
	}
	if len(receipt.Files) != len(names) {
		return nil, errors.New("collection receipt browser artifact mismatch")
	}
	source := &cachedArchiveSource{root: root, receipt: receipt, names: names}
	optData, _, optErr := readArchiveFileAtRootLimitedContext(ctx, root, collectionOptionalReceiptFileName, collectionReceiptMaxBytes)
	if optErr != nil && !os.IsNotExist(optErr) {
		return nil, optErr
	}
	if optErr == nil {
		var optReceipt collectionOptionalReceipt
		if err := json.Unmarshal(optData, &optReceipt); err != nil {
			return nil, err
		}
		if optReceipt.TestID != c.intent.TestID || optReceipt.Token != c.intent.IntentToken || optReceipt.Target != target || optReceipt.Identity != c.intent.Sources[target] || optReceipt.ReceiptSHA256 != anchor.ReceiptSHA256 || len(optReceipt.Files) == 0 || len(optReceipt.Files) > maxCollectionOptionalFiles {
			return nil, errors.New("collection optional receipt identity mismatch")
		}
		declaredOptional := make(map[string]RunArchiveEvidenceArtifact, len(manifest.BrowserHARArtifacts)+1)
		for _, har := range manifest.BrowserHARArtifacts {
			if validDeclaredHAR(har) {
				declaredOptional[har.Name] = har
			}
		}
		if validDeclaredInteractions(manifest.BrowserInteractionsArtifact) {
			declaredOptional[manifest.BrowserInteractionsArtifact.Name] = *manifest.BrowserInteractionsArtifact
		}
		seenOptional := make(map[string]bool, len(optReceipt.Files))
		optTotal := total + int64(len(optData))
		for _, file := range optReceipt.Files {
			declared, ok := declaredOptional[file.Name]
			if !ok || seenOptional[file.Name] || file.Bytes != declared.Bytes || file.SHA256 != declared.SHA256 || file.Bytes <= 0 || file.Bytes > c.intent.CacheLimitBytes-optTotal {
				return nil, errors.New("invalid collection optional receipt payload")
			}
			seenOptional[file.Name] = true
			optTotal += file.Bytes
		}
		if optTotal > c.intent.CacheLimitBytes {
			return nil, errors.New("collection optional receipt size mismatch")
		}
		source.optionalFiles = optReceipt.Files
		source.hasOptionalReceipt = true
	}
	return source, nil
}

type cachedArchiveSource struct {
	root               *os.Root
	receipt            collectionSourceReceipt
	names              []string
	optionalFiles      []collectionCachedFile
	hasOptionalReceipt bool
}

func (s *cachedArchiveSource) hasOptional(name string) bool {
	if s == nil {
		return false
	}
	for _, file := range s.optionalFiles {
		if file.Name == name {
			return true
		}
	}
	return false
}

func (s *cachedArchiveSource) close() { _ = s.root.Close() }
func (s *cachedArchiveSource) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, errors.New("cached collection sources require streaming")
}
func (s *cachedArchiveSource) StreamArchiveArtifact(ctx context.Context, request generatorrpc.ArchiveArtifactRequest, dst io.Writer, max int64) (int64, error) {
	if request.TestID != s.receipt.TestID || request.SourceIdentity != s.receipt.Identity {
		return 0, errors.New("cached archive source binding mismatch")
	}
	if request.Name == runArchiveManifestFileName {
		if int64(len(s.receipt.Manifest)) > max {
			return 0, errors.New("cached manifest exceeds limit")
		}
		return copyArchiveArtifactContext(ctx, dst, bytes.NewReader(s.receipt.Manifest))
	}
	for i, name := range s.names {
		if request.Name == name {
			return s.streamVerifiedFile(ctx, s.receipt.Files[i], dst, max)
		}
	}
	for _, expected := range s.optionalFiles {
		if request.Name == expected.Name {
			return s.streamVerifiedFile(ctx, expected, dst, max)
		}
	}
	return 0, errors.New("unknown cached artifact")
}

func (s *cachedArchiveSource) streamVerifiedFile(ctx context.Context, expected collectionCachedFile, dst io.Writer, max int64) (int64, error) {
	file, info, err := openArchiveArtifactForReadAt(s.root, expected.Name)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if info.Size() != expected.Bytes || expected.Bytes > max {
		return 0, errors.New("cached artifact size mismatch")
	}
	hash := sha256.New()
	n, err := copyArchiveArtifactContext(ctx, io.MultiWriter(dst, hash), io.LimitReader(file, expected.Bytes+1))
	if err != nil {
		return n, err
	}
	if n != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return n, errors.New("cached artifact hash mismatch")
	}
	return n, nil
}

// Includes incomplete/unanchored files: a crash or SQL cascade cannot erase
// their disk cost. Descriptor traversal rejects symlinks and bounds scan work.
func collectionCacheUsage(ctx context.Context, root *os.Root, max int64) (int64, error) {
	entries := 0
	var walk func(*os.Root, int) (int64, error)
	walk = func(dir *os.Root, depth int) (int64, error) {
		if depth > 6 {
			return 0, errors.New("collection cache depth exceeded")
		}
		fd, err := dir.Open(".")
		if err != nil {
			return 0, err
		}
		defer fd.Close()
		var total int64
		for {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			batch, err := fd.ReadDir(128)
			if err != nil && err != io.EOF {
				return 0, err
			}
			for _, entry := range batch {
				entries++
				if entries > 100000 {
					return 0, errors.New("collection cache scan limit exceeded")
				}
				info, e := dir.Lstat(entry.Name())
				if e != nil {
					return 0, e
				}
				var size int64
				if info.IsDir() {
					child, e := openArchiveChildDirectoryAt(dir, entry.Name(), false, 0)
					if e != nil {
						return 0, e
					}
					size, e = walk(child, depth+1)
					_ = child.Close()
					if e != nil {
						return 0, e
					}
				} else if info.Mode().IsRegular() {
					size = info.Size()
				} else {
					return 0, errors.New("collection cache contains a non-regular entry")
				}
				if size < 0 || size > max-total {
					return 0, errors.New("collection cache capacity exceeded")
				}
				total += size
			}
			if err == io.EOF {
				return total, nil
			}
		}
	}
	return walk(root, 0)
}
func (c *collectionSourceCache) save(ctx context.Context, bundle remoteArchiveBundle) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.intent.Sources[bundle.target] == "" {
		return errors.New("collection source set changed during attempt")
	}
	receipt := collectionSourceReceipt{TestID: c.intent.TestID, ProjectID: c.intent.ProjectID, StartTime: c.intent.StartTime, Token: c.intent.IntentToken, Target: bundle.target, Identity: c.intent.Sources[bundle.target], Manifest: bundle.manifestData}
	if err := validateBrowserDocumentManifest(&bundle.manifest, true); err != nil {
		return err
	}
	paths := []string{bundle.transactionsPath, bundle.resourcesPath, bundle.summariesPath}
	if bundle.manifest.BrowserReceiptsFile != "" {
		if bundle.browserReceiptsPath == "" {
			return errors.New("collection browser receipt source is missing")
		}
		paths = append(paths, bundle.browserReceiptsPath)
	} else if bundle.browserReceiptsPath != "" {
		return errors.New("undeclared collection browser receipt source")
	}
	if bundle.manifest.BrowserDocumentsFile != "" {
		if bundle.browserDocumentsPath == "" {
			return errors.New("collection browser source is missing")
		}
		paths = append(paths, bundle.browserDocumentsPath)
	} else if bundle.browserDocumentsPath != "" {
		return errors.New("undeclared collection browser source")
	}
	cacheNames := collectionBrowserPayloadNames(&bundle.manifest)
	var payloadBytes int64
	// Transfer files are private, but retain descriptors so hashing and copying
	// cannot switch to a different pathname after validation.
	files := make([]*os.File, 0, len(paths))
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	for i, path := range paths {
		file, _, err := openArchiveArtifactForRead(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return err
		}
		files = append(files, file)
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("invalid downloaded collection file")
		}
		if info.Size() < 0 || info.Size() > c.intent.CacheLimitBytes-payloadBytes {
			return errors.New("collection source exceeds run limit")
		}
		hash := sha256.New()
		n, err := copyArchiveArtifactContext(ctx, hash, io.LimitReader(file, info.Size()+1))
		if err != nil {
			return err
		}
		if n != info.Size() {
			return errors.New("downloaded collection file changed")
		}
		payloadBytes += n
		receipt.Files = append(receipt.Files, collectionCachedFile{Name: cacheNames[i], Bytes: n, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if int64(len(data)) > collectionReceiptMaxBytes {
		return errors.New("collection receipt exceeds limit")
	}
	size := payloadBytes + int64(len(data))
	anchor := storage.RunArchiveCollectionSource{ReceiptSHA256: sha256BytesHex(data), Bytes: size}
	remaining := c.intent.CacheLimitBytes
	for target, source := range c.intent.CachedSources {
		if target == bundle.target {
			if source != anchor {
				return errors.New("refetched collection source differs from its durable receipt")
			}
			continue // One logical source may have a verified copy in each local root.
		}
		remaining -= source.Bytes
	}
	if size > remaining {
		return errors.New("collection cache exceeds frozen run limit")
	}
	release, err := lockArchiveNamespace(ctx, "_collection_locks", "capacity.lock")
	if err != nil {
		return err
	}
	defer release()
	used, err := collectionCacheUsage(ctx, c.root, c.maxBytes)
	if err != nil {
		return err
	}
	parent, err := c.targetRoot(bundle.target, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	destination, err := openArchiveChildDirectoryAt(parent, anchor.ReceiptSHA256, false, 0)
	if os.IsNotExist(err) {
		if size > c.maxBytes-used {
			return errors.New("collection cache capacity exceeded")
		}
		available, err := diskspace.AvailablePath(resolveArchiveRoot())
		if err != nil {
			return err
		}
		if available < size+(64<<20) {
			return errors.New("insufficient free space for archive collection cache")
		}
		var runUsed int64
		hasRunUsed := false
		if runRoot, runErr := openArchiveChildDirectoryAt(c.root, c.intent.TestID, false, 0); runErr == nil {
			if measuredRunUsed, usageErr := collectionCacheUsage(ctx, runRoot, c.intent.CacheLimitBytes); usageErr == nil {
				runUsed = measuredRunUsed
				hasRunUsed = true
			}
			_ = runRoot.Close()
		}
		stage := ".pending-" + uuid.NewString()
		if err := parent.Mkdir(stage, 0o700); err != nil {
			return err
		}
		destination, err = openArchiveChildDirectoryAt(parent, stage, false, 0)
		if err != nil {
			return err
		}
		defer destination.Close()
		// Crash leftovers remain charged against quota. A failed local copy
		// can be removed; the original downloaded source has not been changed.
		defer func() { _ = parent.RemoveAll(stage) }()
		for i, source := range files {
			if _, err := source.Seek(0, io.SeekStart); err != nil {
				return err
			}
			dst, err := destination.OpenFile(cacheNames[i], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			hash := sha256.New()
			n, copyErr := copyArchiveArtifactContext(ctx, io.MultiWriter(dst, hash), io.LimitReader(source, receipt.Files[i].Bytes+1))
			syncErr := dst.Sync()
			closeErr := dst.Close()
			if copyErr != nil {
				return copyErr
			}
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
			if n != receipt.Files[i].Bytes || hex.EncodeToString(hash.Sum(nil)) != receipt.Files[i].SHA256 {
				return errors.New("downloaded collection source changed during copy")
			}
		}
		file, err := destination.OpenFile("receipt.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if hasRunUsed {
			c.stageOptional(ctx, destination, bundle, receipt.Identity, anchor.ReceiptSHA256, used+size, runUsed+size)
		}
		if err := syncOpenedArchiveDirectory(destination); err != nil {
			return err
		}
		if err := parent.Rename(stage, anchor.ReceiptSHA256); err != nil {
			return err
		}
		if err := syncOpenedArchiveDirectory(parent); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		defer destination.Close()
		cached, err := c.decode(ctx, destination, bundle.target, anchor)
		if err != nil {
			return err
		}
		for _, name := range cached.names {
			if _, err := cached.StreamArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: c.intent.TestID, SourceIdentity: receipt.Identity, Name: name}, io.Discard, c.intent.CacheLimitBytes); err != nil {
				return err
			}
		}
		for _, opt := range cached.optionalFiles {
			if _, err := cached.StreamArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: c.intent.TestID, SourceIdentity: receipt.Identity, Name: opt.Name}, io.Discard, c.intent.CacheLimitBytes); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var saved bool
	err = c.tr.trackDurableWrite(func() error {
		var err error
		saved, err = c.store.SaveRunArchiveCollectionSource(c.intent.TestID, c.intent.IntentToken, bundle.target, anchor, c.lease, c.owner)
		return err
	})
	if err != nil {
		return err
	}
	if !saved {
		return errors.New("collection cache receipt was fenced")
	}
	if c.intent.CachedSources == nil {
		c.intent.CachedSources = make(map[string]storage.RunArchiveCollectionSource)
	}
	c.intent.CachedSources[bundle.target] = anchor
	return nil
}

// stageOptional best-effort stages optional HAR and interaction files alongside
// required source files inside the pending stage directory before atomic rename,
// recording their digests in optional_receipt.json so SQL ReceiptSHA256 never
// diverges across HA pods with different local disk space and the committed
// ReceiptSHA256 directory is immutable once renamed. Exceeding byte or
// disk-space bounds skips optional files without failing the collection attempt.
func (c *collectionSourceCache) stageOptional(ctx context.Context, destination *os.Root, bundle remoteArchiveBundle, identity, receiptSHA256 string, used, runUsed int64) {
	if c == nil || destination == nil || (len(bundle.hars) == 0 && bundle.interactions == nil) {
		return
	}
	if ctx.Err() != nil || identity == "" || receiptSHA256 == "" {
		return
	}
	type optionalCandidate struct {
		name   string
		path   string
		bytes  int64
		sha256 string
	}
	candidates := make([]optionalCandidate, 0, len(bundle.hars)+1)
	for _, har := range bundle.hars {
		if validDeclaredHAR(har.entry) && har.path != "" {
			candidates = append(candidates, optionalCandidate{
				name:   har.entry.Name,
				path:   har.path,
				bytes:  har.entry.Bytes,
				sha256: har.entry.SHA256,
			})
		}
	}
	if bundle.interactions != nil && validDeclaredInteractions(&bundle.interactions.entry) && bundle.interactions.path != "" {
		candidates = append(candidates, optionalCandidate{
			name:   bundle.interactions.entry.Name,
			path:   bundle.interactions.path,
			bytes:  bundle.interactions.entry.Bytes,
			sha256: bundle.interactions.entry.SHA256,
		})
	}
	if len(candidates) == 0 || len(candidates) > maxCollectionOptionalFiles {
		return
	}
	var optFiles []collectionCachedFile
	var writtenNames []string
	committed := false
	defer func() {
		if !committed {
			_ = destination.Remove(collectionOptionalReceiptFileName)
			for _, name := range writtenNames {
				_ = destination.Remove(name)
			}
		}
	}()
	var optBytes int64
	seen := make(map[string]bool, len(candidates))
	for _, cand := range candidates {
		if ctx.Err() != nil {
			return
		}
		if seen[cand.name] || cand.bytes <= 0 {
			continue
		}
		trialFiles := append(append([]collectionCachedFile(nil), optFiles...), collectionCachedFile{Name: cand.name, Bytes: cand.bytes, SHA256: cand.sha256})
		trialReceipt, marshalErr := json.Marshal(collectionOptionalReceipt{
			TestID:        c.intent.TestID,
			Token:         c.intent.IntentToken,
			Target:        bundle.target,
			Identity:      identity,
			ReceiptSHA256: receiptSHA256,
			Files:         trialFiles,
		})
		if marshalErr != nil || int64(len(trialReceipt)) > collectionReceiptMaxBytes {
			continue
		}
		needed := optBytes + cand.bytes + int64(len(trialReceipt))
		if needed > c.intent.CacheLimitBytes-runUsed || needed > c.maxBytes-used {
			continue
		}
		available, availErr := diskspace.AvailablePath(resolveArchiveRoot())
		if availErr != nil || available < cand.bytes+int64(len(trialReceipt))+(64<<20) {
			continue
		}
		src, info, openErr := openArchiveArtifactForRead(filepath.Dir(cand.path), filepath.Base(cand.path))
		if openErr != nil {
			continue
		}
		if !info.Mode().IsRegular() || info.Size() != cand.bytes {
			_ = src.Close()
			continue
		}
		dst, createErr := destination.OpenFile(cand.name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			_ = src.Close()
			continue
		}
		hash := sha256.New()
		n, copyErr := copyArchiveArtifactContext(ctx, io.MultiWriter(dst, hash), io.LimitReader(src, cand.bytes+1))
		syncErr := dst.Sync()
		closeErr := dst.Close()
		_ = src.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || n != cand.bytes || hex.EncodeToString(hash.Sum(nil)) != cand.sha256 {
			_ = destination.Remove(cand.name)
			continue
		}
		seen[cand.name] = true
		writtenNames = append(writtenNames, cand.name)
		optFiles = append(optFiles, collectionCachedFile{Name: cand.name, Bytes: cand.bytes, SHA256: cand.sha256})
		optBytes += cand.bytes
	}
	if len(optFiles) == 0 {
		return
	}
	optReceipt := collectionOptionalReceipt{
		TestID:        c.intent.TestID,
		Token:         c.intent.IntentToken,
		Target:        bundle.target,
		Identity:      identity,
		ReceiptSHA256: receiptSHA256,
		Files:         optFiles,
	}
	data, err := json.Marshal(optReceipt)
	if err != nil || int64(len(data)) > collectionReceiptMaxBytes {
		return
	}
	totalOptionalBytes := optBytes + int64(len(data))
	if totalOptionalBytes > c.intent.CacheLimitBytes-runUsed || totalOptionalBytes > c.maxBytes-used {
		return
	}
	file, err := destination.OpenFile(collectionOptionalReceiptFileName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return
	}
	committed = true
}
