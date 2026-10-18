// Package archiveingest provides bounded, path-safe extraction of untrusted
// archives. It deliberately returns in-memory files instead of writing to disk,
// so callers retain their existing source-root selection behavior without
// creating a second path-validation boundary.
package archiveingest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// LogicalPath normalizes an importer-owned path using archive semantics. It is
// not a security validator; untrusted paths must still pass ValidateFilePath.
func LogicalPath(value string) string {
	return path.Clean(strings.ReplaceAll(strings.TrimSpace(value), `\`, "/"))
}

func LogicalBase(value string) string { return path.Base(LogicalPath(value)) }
func LogicalDir(value string) string  { return path.Dir(LogicalPath(value)) }
func LogicalExt(value string) string  { return path.Ext(LogicalPath(value)) }
func LogicalJoin(values ...string) string {
	for index := range values {
		values[index] = strings.ReplaceAll(values[index], `\`, "/")
	}
	return path.Join(values...)
}

const (
	defaultMaxEntries    = 4096
	defaultMaxEntryBytes = int64(64 << 20)
	defaultMaxTotalBytes = int64(256 << 20)
	defaultMaxDepth      = 32
	defaultMaxRatio      = int64(1_000)
	maxTARMetadataBytes  = int64(32 << 20)
	tarMetadataPerEntry  = int64(8 << 10)
	tarEndRecordBytes    = int64(2 * 512)
)

// Limits bounds both archive metadata work and decompressed bytes.
type Limits struct {
	MaxEntries          int
	MaxEntryBytes       int64
	MaxTotalBytes       int64
	MaxDepth            int
	MaxCompressionRatio int64
}

// Options controls extraction. StripFirstComponent removes a disposable
// project container only when every regular file shares that same archive-root
// component. Mixed-root archives retain every validated relative path.
//
// Operating-system and version-control files (JunkReason) are never content:
// the readers leave them out unread, before the common root is found, so a
// __MACOSX folder neither becomes a source file nor keeps the container.
type Options struct {
	Limits              Limits
	StripFirstComponent bool
}

// File is one validated regular file from an archive.
type File struct {
	Name string
	Data []byte
}

// ValidateFilePath applies the same path policy used by the ZIP and TAR
// readers to a regular-file name supplied by an adapter-specific archive
// reader. It is intentionally exported so importers that must temporarily
// retain source-specific archive semantics (for example, file modes or
// compression-ratio accounting) do not grow a second path policy.
func ValidateFilePath(raw string, maxDepth int) (string, error) {
	limits, err := normalizedLimits(Limits{MaxDepth: maxDepth})
	if err != nil {
		return "", err
	}
	name, isDirectory, err := validatePath(raw, false, Options{}, limits)
	if err != nil {
		return "", err
	}
	if isDirectory {
		return "", errors.New("entry path names a directory, not a regular file")
	}
	return name, nil
}

// ValidateFilePaths validates an ordered set of regular-file names and rejects
// exact, separator-normalized, case-folded, and NFC-equivalent collisions. The
// returned names retain the caller's order so adapters can sort source map keys
// first and obtain deterministic validation errors.
func ValidateFilePaths(raw []string, maxDepth int) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]string, len(raw))
	for _, candidate := range raw {
		name, err := ValidateFilePath(candidate, maxDepth)
		if err != nil {
			return nil, fmt.Errorf("unsafe file path %q: %w", safeDisplayName(candidate), err)
		}
		if err := recordName(seen, name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, nil
}

// ReadZIP validates and extracts regular files from a ZIP archive.
func ReadZIP(data []byte, options Options) ([]File, error) {
	limits, err := normalizedLimits(options.Limits)
	if err != nil {
		return nil, err
	}
	if err := ValidateZIPDirectory(data, limits.MaxEntries); err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(reader.File) > limits.MaxEntries {
		return nil, fmt.Errorf("archive has too many entries (%d > maximum %d)", len(reader.File), limits.MaxEntries)
	}

	result := make([]File, 0, len(reader.File))
	seen := make(map[string]string, len(reader.File))
	var total int64
	for _, entry := range reader.File {
		name, isDirectory, err := validatePath(entry.Name, entry.NonUTF8, options, limits)
		if err != nil {
			return nil, fmt.Errorf("unsafe archive entry %q: %w", safeDisplayName(entry.Name), err)
		}
		mode := entry.Mode()
		if isDirectory || mode.IsDir() {
			if !isDirectory || !mode.IsDir() {
				return nil, fmt.Errorf("unsafe archive entry %q: directory metadata does not match its path", safeDisplayName(entry.Name))
			}
			continue
		}
		if JunkReason(name) != "" {
			continue
		}
		if mode.Type() != 0 {
			return nil, fmt.Errorf("unsafe archive entry %q: only regular files are permitted", safeDisplayName(entry.Name))
		}
		if err := recordName(seen, name); err != nil {
			return nil, err
		}
		declared := int64(entry.UncompressedSize64)
		if declared < 0 || declared > limits.MaxEntryBytes {
			return nil, fmt.Errorf("archive entry %q exceeds the maximum allowed uncompressed size of %d bytes (decompression limit)", name, limits.MaxEntryBytes)
		}
		if exceedsZIPCompressionRatio(
			entry.UncompressedSize64,
			entry.CompressedSize64,
			limits.MaxCompressionRatio,
		) {
			return nil, fmt.Errorf("archive entry %q exceeds the maximum compression-ratio limit of %d", name, limits.MaxCompressionRatio)
		}
		if declared > limits.MaxTotalBytes-total {
			return nil, fmt.Errorf("archive exceeds the maximum allowed total uncompressed size of %d bytes", limits.MaxTotalBytes)
		}
		body, err := readZIPEntry(entry, limits.MaxEntryBytes, limits.MaxTotalBytes-total)
		if err != nil {
			return nil, fmt.Errorf("read zip entry %q: %w", name, err)
		}
		if int64(len(body)) != declared {
			return nil, fmt.Errorf("archive entry %q declared %d uncompressed bytes but produced %d", name, declared, len(body))
		}
		total += int64(len(body))
		result = append(result, File{Name: name, Data: body})
	}
	return finalizeArchivePaths(result, options, limits)
}

// ReadTAR validates and extracts regular files from an uncompressed TAR stream.
// Compression wrappers, when desired, must themselves be bounded by the caller.
func ReadTAR(reader io.Reader, options Options) ([]File, error) {
	limits, err := normalizedLimits(options.Limits)
	if err != nil {
		return nil, err
	}
	inputLimit := tarInputLimit(limits)
	bounded := &io.LimitedReader{R: reader, N: inputLimit}
	tr := tar.NewReader(bounded)
	result := make([]File, 0)
	seen := make(map[string]string)
	var total int64
	entries := 0
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if bounded.N == 0 {
				return nil, fmt.Errorf("archive exceeds the maximum TAR input size of %d bytes", inputLimit)
			}
			return nil, fmt.Errorf("read archive header: %w", err)
		}
		entries++
		if entries > limits.MaxEntries {
			return nil, fmt.Errorf("archive has too many entries (> maximum %d)", limits.MaxEntries)
		}
		name, isDirectory, err := validatePath(header.Name, false, options, limits)
		if err != nil {
			return nil, fmt.Errorf("unsafe archive entry %q: %w", safeDisplayName(header.Name), err)
		}
		if header.Typeflag == tar.TypeDir {
			if !isDirectory {
				return nil, fmt.Errorf("unsafe archive entry %q: directory metadata does not match its path", safeDisplayName(header.Name))
			}
			continue
		}
		if !isDirectory && JunkReason(name) != "" {
			continue
		}
		// tar.Reader reports legacy TypeRegA entries as TypeReg.
		if isDirectory || header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unsafe archive entry %q: only regular files are permitted", safeDisplayName(header.Name))
		}
		if err := recordName(seen, name); err != nil {
			return nil, err
		}
		if header.Size < 0 || header.Size > limits.MaxEntryBytes {
			return nil, fmt.Errorf("archive entry %q exceeds the maximum allowed uncompressed size of %d bytes (decompression limit)", name, limits.MaxEntryBytes)
		}
		if header.Size > limits.MaxTotalBytes-total {
			return nil, fmt.Errorf("archive exceeds the maximum allowed total uncompressed size of %d bytes", limits.MaxTotalBytes)
		}
		body, err := io.ReadAll(io.LimitReader(tr, header.Size+1))
		if err != nil {
			return nil, fmt.Errorf("read archive entry %q: %w", name, err)
		}
		if int64(len(body)) != header.Size {
			return nil, fmt.Errorf("archive entry %q declared %d bytes but produced %d", name, header.Size, len(body))
		}
		total += int64(len(body))
		result = append(result, File{Name: name, Data: body})
	}
	return finalizeArchivePaths(result, options, limits)
}

func tarInputLimit(limits Limits) int64 {
	metadata := int64(limits.MaxEntries)
	if metadata > maxTARMetadataBytes/tarMetadataPerEntry {
		metadata = maxTARMetadataBytes
	} else {
		metadata *= tarMetadataPerEntry
	}
	overhead := metadata + tarEndRecordBytes
	maxInt64 := int64(^uint64(0) >> 1)
	if limits.MaxTotalBytes > maxInt64-overhead {
		return maxInt64
	}
	return limits.MaxTotalBytes + overhead
}

// Strings converts extracted files into the representation used by the
// text-based import adapters.
func Strings(files []File) map[string]string {
	result := make(map[string]string, len(files))
	for _, file := range files {
		result[file.Name] = string(file.Data)
	}
	return result
}

func normalizedLimits(limits Limits) (Limits, error) {
	if limits.MaxEntries == 0 {
		limits.MaxEntries = defaultMaxEntries
	}
	if limits.MaxEntryBytes == 0 {
		limits.MaxEntryBytes = defaultMaxEntryBytes
	}
	if limits.MaxTotalBytes == 0 {
		limits.MaxTotalBytes = defaultMaxTotalBytes
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = defaultMaxDepth
	}
	if limits.MaxCompressionRatio == 0 {
		limits.MaxCompressionRatio = defaultMaxRatio
	}
	if limits.MaxEntries < 1 ||
		limits.MaxEntryBytes < 1 ||
		limits.MaxTotalBytes < 1 ||
		limits.MaxDepth < 1 ||
		limits.MaxCompressionRatio < 1 {
		return Limits{}, errors.New("archive limits must be positive")
	}
	if limits.MaxEntryBytes > limits.MaxTotalBytes {
		limits.MaxEntryBytes = limits.MaxTotalBytes
	}
	return limits, nil
}

func exceedsZIPCompressionRatio(expanded, compressed uint64, maximum int64) bool {
	if expanded == 0 {
		return false
	}
	if compressed == 0 || maximum < 1 {
		return true
	}
	limit := uint64(maximum)
	quotient := expanded / compressed
	return quotient > limit || (quotient == limit && expanded%compressed != 0)
}

func readZIPEntry(entry *zip.File, maxEntry, remaining int64) ([]byte, error) {
	limit := maxEntry
	if remaining < limit {
		limit = remaining
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(body)) > limit {
		return nil, errors.New("decompression limit exceeded (possible archive bomb)")
	}
	return body, nil
}

func validatePath(raw string, declaredNonUTF8 bool, options Options, limits Limits) (string, bool, error) {
	if declaredNonUTF8 || !utf8.ValidString(raw) {
		return "", false, errors.New("entry name is not valid UTF-8")
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", false, errors.New("entry name contains control characters")
		}
	}
	if raw == "" {
		return "", false, errors.New("entry name is empty")
	}
	normalizedSeparators := strings.ReplaceAll(raw, `\`, "/")
	if strings.HasPrefix(normalizedSeparators, "/") || strings.HasPrefix(normalizedSeparators, "//") || hasDrivePrefix(normalizedSeparators) {
		return "", false, errors.New("absolute and drive-qualified paths are not permitted")
	}
	isDirectory := strings.HasSuffix(normalizedSeparators, "/")
	trimmed := strings.TrimSuffix(normalizedSeparators, "/")
	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if part == "" {
			return "", false, errors.New("entry path contains an empty component")
		}
		if part == "." || part == ".." {
			return "", false, errors.New("entry path contains a dot or traversal component")
		}
		if strings.TrimSpace(part) != part {
			return "", false, errors.New("entry path contains a component with leading or trailing whitespace")
		}
	}
	if len(parts) == 0 {
		return "", false, errors.New("entry path is empty after normalization")
	}
	maxDepth := limits.MaxDepth
	if options.StripFirstComponent && maxDepth < int(^uint(0)>>1) {
		// A common disposable root does not count toward the caller's final path
		// depth. finalizeArchivePaths rechecks the post-strip (or unstripped
		// mixed-root) names against the exact configured limit.
		maxDepth++
	}
	if len(parts) > maxDepth {
		return "", false, fmt.Errorf("entry path depth exceeds %d", limits.MaxDepth)
	}
	return strings.Join(parts, "/"), isDirectory, nil
}

func finalizeArchivePaths(files []File, options Options, limits Limits) ([]File, error) {
	if len(files) == 0 || !options.StripFirstComponent {
		return files, nil
	}
	commonRoot := ""
	for _, file := range files {
		parts := strings.Split(file.Name, "/")
		if len(parts) < 2 {
			commonRoot = ""
			break
		}
		if commonRoot == "" {
			commonRoot = parts[0]
			continue
		}
		if parts[0] != commonRoot {
			commonRoot = ""
			break
		}
	}

	seen := make(map[string]string, len(files))
	for index := range files {
		name := files[index].Name
		if commonRoot != "" {
			name = strings.TrimPrefix(name, commonRoot+"/")
		}
		if len(strings.Split(name, "/")) > limits.MaxDepth {
			return nil, fmt.Errorf("archive entry %q path depth exceeds %d after common-root evaluation", name, limits.MaxDepth)
		}
		if err := recordName(seen, name); err != nil {
			return nil, err
		}
		files[index].Name = name
	}
	return files, nil
}

func recordName(seen map[string]string, name string) error {
	canonical := cases.Fold().String(norm.NFC.String(name))
	if previous, exists := seen[canonical]; exists {
		if previous == name {
			return fmt.Errorf("archive contains duplicate normalized entry %q", name)
		}
		return fmt.Errorf("archive contains case-insensitive duplicate or otherwise colliding normalized entries %q and %q", previous, name)
	}
	seen[canonical] = name
	return nil
}

func hasDrivePrefix(name string) bool {
	return len(name) >= 2 &&
		((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) &&
		name[1] == ':'
}

func safeDisplayName(name string) string {
	if utf8.ValidString(name) {
		return name
	}
	return "<invalid-utf8>"
}
