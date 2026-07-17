package bruno

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type sourceFile struct {
	Path string
	Data []byte
	Mode int64
}

// Inspect intentionally retains the normalized source, a sanitized copy, raw
// and sanitized deterministic archives, parser state, and short-lived input
// buffers at the same time. Keep the expanded source to a conservative share
// of the admitted materialization envelope instead of treating decompressed
// bytes as the only memory cost.
const brunoMaterializationExpansionFactor int64 = 32

func loadSourceFiles(input Input) ([]sourceFile, []byte, error) {
	limits := normalizeLimits(input.Limits)
	if len(input.Files) > 0 {
		files := make([]sourceFile, 0, len(input.Files))
		for name, data := range input.Files {
			files = append(files, sourceFile{Path: name, Data: append([]byte(nil), data...), Mode: 0o644})
		}
		normalized, err := validateAndNormalizeFiles(files, limits)
		return normalized, nil, err
	}
	if len(input.Content) == 0 {
		return nil, nil, errors.New("Bruno source content is required")
	}
	filename := strings.TrimSpace(input.Filename)
	if filename == "" {
		filename = "request.yml"
	}
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".zip") || bytes.HasPrefix(input.Content, []byte{'P', 'K', 3, 4}):
		files, err := loadZIP(input.Content, limits)
		return files, append([]byte(nil), input.Content...), err
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"), isGZIP(input.Content):
		files, err := loadTAR(input.Content, true, limits)
		return files, append([]byte(nil), input.Content...), err
	case strings.HasSuffix(lower, ".tar"):
		files, err := loadTAR(input.Content, false, limits)
		return files, append([]byte(nil), input.Content...), err
	default:
		files, err := validateAndNormalizeFiles([]sourceFile{{Path: path.Base(filename), Data: append([]byte(nil), input.Content...), Mode: 0o644}}, limits)
		return files, append([]byte(nil), input.Content...), err
	}
}

func normalizeLimits(value Limits) Limits {
	defaults := DefaultLimits()
	if value.MaxFiles <= 0 {
		value.MaxFiles = defaults.MaxFiles
	}
	if value.MaxFileBytes <= 0 {
		value.MaxFileBytes = defaults.MaxFileBytes
	}
	if value.MaxExpandedBytes <= 0 {
		value.MaxExpandedBytes = defaults.MaxExpandedBytes
	}
	if value.MaxMaterializedBytes <= 0 {
		value.MaxMaterializedBytes = defaults.MaxMaterializedBytes
	}
	materializedExpandedLimit := value.MaxMaterializedBytes / brunoMaterializationExpansionFactor
	if materializedExpandedLimit < 1 {
		materializedExpandedLimit = 1
	}
	if value.MaxExpandedBytes > materializedExpandedLimit {
		value.MaxExpandedBytes = materializedExpandedLimit
	}
	if value.MaxPathDepth <= 0 {
		value.MaxPathDepth = defaults.MaxPathDepth
	}
	if value.MaxCompressionRatio <= 0 {
		value.MaxCompressionRatio = defaults.MaxCompressionRatio
	}
	return value
}

func loadZIP(data []byte, limits Limits) ([]sourceFile, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open Bruno ZIP: %w", err)
	}
	if len(reader.File) > limits.MaxFiles {
		return nil, fmt.Errorf("Bruno archive contains %d files; maximum is %d", len(reader.File), limits.MaxFiles)
	}
	files := make([]sourceFile, 0, len(reader.File))
	var total int64
	var actualTotal int64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		mode := entry.Mode()
		if mode.Type() != 0 {
			return nil, fmt.Errorf("Bruno archive entry %q is not a regular file", entry.Name)
		}
		if entry.UncompressedSize64 > uint64(limits.MaxFileBytes) || entry.UncompressedSize64 > uint64(^uint64(0)>>1) {
			return nil, fmt.Errorf("Bruno archive entry %q exceeds the per-file limit", entry.Name)
		}
		size := int64(entry.UncompressedSize64)
		if size > limits.MaxFileBytes {
			return nil, fmt.Errorf("Bruno archive entry %q exceeds the per-file limit", entry.Name)
		}
		if exceedsCompressionRatio(size, int64(entry.CompressedSize64), limits.MaxCompressionRatio) {
			return nil, fmt.Errorf("Bruno archive entry %q exceeds the compression-ratio limit", entry.Name)
		}
		if exceedsByteBudget(total, size, limits.MaxExpandedBytes) {
			return nil, fmt.Errorf("Bruno archive expanded size exceeds %d bytes", limits.MaxExpandedBytes)
		}
		total += size
		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("open Bruno archive entry %q: %w", entry.Name, err)
		}
		content, readErr := readBounded(rc, limits.MaxFileBytes)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Bruno archive entry %q: %w", entry.Name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close Bruno archive entry %q: %w", entry.Name, closeErr)
		}
		if int64(len(content)) != size {
			return nil, fmt.Errorf("Bruno archive entry %q expanded size does not match its declaration", entry.Name)
		}
		// Count bytes actually materialized, not only attacker-controlled headers.
		if exceedsByteBudget(actualTotal, int64(len(content)), limits.MaxExpandedBytes) {
			return nil, fmt.Errorf("Bruno archive expanded size exceeds %d bytes", limits.MaxExpandedBytes)
		}
		actualTotal += int64(len(content))
		files = append(files, sourceFile{Path: entry.Name, Data: content, Mode: int64(mode.Perm())})
	}
	return validateAndNormalizeFiles(files, limits)
}

func loadTAR(data []byte, compressed bool, limits Limits) ([]sourceFile, error) {
	var source io.Reader = bytes.NewReader(data)
	if compressed {
		reader, err := gzip.NewReader(source)
		if err != nil {
			return nil, fmt.Errorf("open Bruno tar.gz: %w", err)
		}
		defer reader.Close()
		source = reader
	}
	reader := tar.NewReader(source)
	files := make([]sourceFile, 0)
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read Bruno tar header: %w", err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return nil, fmt.Errorf("Bruno archive entry %q is not a regular file", header.Name)
		}
		if len(files) >= limits.MaxFiles {
			return nil, fmt.Errorf("Bruno archive contains more than %d files", limits.MaxFiles)
		}
		if header.Size < 0 || header.Size > limits.MaxFileBytes {
			return nil, fmt.Errorf("Bruno archive entry %q exceeds the per-file limit", header.Name)
		}
		if exceedsByteBudget(total, header.Size, limits.MaxExpandedBytes) {
			return nil, fmt.Errorf("Bruno archive expanded size exceeds %d bytes", limits.MaxExpandedBytes)
		}
		total += header.Size
		if compressed && exceedsCompressionRatio(total, int64(len(data)), limits.MaxCompressionRatio) {
			return nil, fmt.Errorf("Bruno tar.gz exceeds the compression-ratio limit")
		}
		content, err := readBounded(io.LimitReader(reader, header.Size), limits.MaxFileBytes)
		if err != nil {
			return nil, fmt.Errorf("read Bruno archive entry %q: %w", header.Name, err)
		}
		files = append(files, sourceFile{Path: header.Name, Data: content, Mode: header.Mode & 0o777})
	}
	if compressed && exceedsCompressionRatio(total, int64(len(data)), limits.MaxCompressionRatio) {
		return nil, fmt.Errorf("Bruno tar.gz exceeds the compression-ratio limit")
	}
	return validateAndNormalizeFiles(files, limits)
}

func validateAndNormalizeFiles(files []sourceFile, limits Limits) ([]sourceFile, error) {
	if len(files) == 0 {
		return nil, errors.New("Bruno bundle contains no files")
	}
	if len(files) > limits.MaxFiles {
		return nil, fmt.Errorf("Bruno bundle contains %d files; maximum is %d", len(files), limits.MaxFiles)
	}
	normalized := make([]sourceFile, 0, len(files))
	seen := make(map[string]string, len(files))
	var total int64
	for _, file := range files {
		cleaned, err := safeRelativePath(file.Path, limits.MaxPathDepth)
		if err != nil {
			return nil, err
		}
		folded := cases.Fold().String(norm.NFC.String(cleaned))
		if prior, exists := seen[folded]; exists {
			return nil, fmt.Errorf("Bruno bundle paths %q and %q collide after normalization", prior, cleaned)
		}
		seen[folded] = cleaned
		if int64(len(file.Data)) > limits.MaxFileBytes {
			return nil, fmt.Errorf("Bruno bundle file %q exceeds %d bytes", cleaned, limits.MaxFileBytes)
		}
		if exceedsByteBudget(total, int64(len(file.Data)), limits.MaxExpandedBytes) {
			return nil, fmt.Errorf("Bruno bundle expanded size exceeds %d bytes", limits.MaxExpandedBytes)
		}
		total += int64(len(file.Data))
		mode := file.Mode & 0o777
		if mode == 0 {
			mode = 0o644
		}
		normalized = append(normalized, sourceFile{Path: cleaned, Data: append([]byte(nil), file.Data...), Mode: mode})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Path < normalized[j].Path })
	return normalized, nil
}

func exceedsByteBudget(total, next, limit int64) bool {
	return total < 0 || next < 0 || limit < 0 || total > limit || next > limit-total
}

func safeRelativePath(value string, maxDepth int) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("Bruno bundle path contains invalid UTF-8")
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("Bruno bundle path %q has leading or trailing whitespace", value)
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") || path.IsAbs(value) {
		return "", fmt.Errorf("Bruno bundle path %q must be relative", value)
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return "", fmt.Errorf("Bruno bundle path %q contains control characters", value)
		}
	}
	cleaned := norm.NFC.String(path.Clean(value))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("Bruno bundle path %q escapes the collection root", value)
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) > maxDepth {
		return "", fmt.Errorf("Bruno bundle path %q exceeds maximum depth %d", value, maxDepth)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("Bruno bundle path %q is invalid", value)
		}
	}
	return cleaned, nil
}

func exceedsCompressionRatio(expanded, compressed, maximum int64) bool {
	if expanded <= 0 {
		return false
	}
	if compressed <= 0 {
		return true
	}
	quotient, remainder := expanded/compressed, expanded%compressed
	return quotient > maximum || (quotient == maximum && remainder > 0)
}

func deterministicTAR(files []sourceFile) ([]byte, error) {
	var out bytes.Buffer
	writer := tar.NewWriter(&out)
	epoch := time.Unix(0, 0).UTC()
	for _, file := range files {
		header := &tar.Header{
			Name:       file.Path,
			Mode:       file.Mode & 0o777,
			Size:       int64(len(file.Data)),
			ModTime:    epoch,
			AccessTime: epoch,
			ChangeTime: epoch,
			Typeflag:   tar.TypeReg,
			Format:     tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := writer.Write(file.Data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func buildManifest(sanitizedFiles []sourceFile, rawArchive, sanitizedArchive []byte) (Manifest, error) {
	manifest := Manifest{
		Version:          ImporterVersion,
		RawArchiveSHA256: sha256Hex(rawArchive),
		BundleSHA256:     sha256Hex(sanitizedArchive),
		Files:            make([]ManifestFile, 0, len(sanitizedFiles)),
	}
	for _, file := range sanitizedFiles {
		role := classifyFileRole(file.Path, file.Data)
		mediaType := mime.TypeByExtension(strings.ToLower(path.Ext(file.Path)))
		if mediaType == "" {
			mediaType = mediaTypeForRole(role)
		}
		manifest.TotalBytes += int64(len(file.Data))
		manifest.Files = append(manifest.Files, ManifestFile{
			Path:       file.Path,
			Role:       role,
			MediaType:  mediaType,
			SizeBytes:  int64(len(file.Data)),
			SHA256:     sha256Hex(file.Data),
			Mode:       file.Mode,
			Executable: file.Mode&0o111 != 0,
		})
	}
	payload, err := json.Marshal(manifest.Files)
	if err != nil {
		return Manifest{}, err
	}
	manifest.SHA256 = sha256Hex(payload)
	return manifest, nil
}

func classifyFileRole(filename string, data []byte) string {
	lower := strings.ToLower(filename)
	base := path.Base(lower)
	switch {
	case base == "opencollection.yml" || base == "collection.bru" || base == "bruno.json":
		return "collection"
	case base == "folder.yml" || base == "folder.bru":
		return "folder"
	case strings.Contains(lower, "/environments/") || strings.HasPrefix(lower, "environments/") || strings.HasSuffix(lower, ".env"):
		return "environment"
	case base == "package.json" || base == "package-lock.json" || base == "npm-shrinkwrap.json" || base == "pnpm-lock.yaml" || base == "yarn.lock":
		return "dependency"
	case strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".cjs") || strings.HasSuffix(lower, ".mjs") || strings.HasSuffix(lower, ".ts"):
		return "script"
	case strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".crt") || strings.HasSuffix(lower, ".cer") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx"):
		return "certificate"
	case isBrunoRequestContent(data, path.Ext(lower)):
		return "request"
	default:
		return "asset"
	}
}

func mediaTypeForRole(role string) string {
	switch role {
	case "collection", "folder", "environment", "request":
		return "application/yaml"
	case "script":
		return "text/javascript"
	case "dependency":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("content exceeds %d bytes", maximum)
	}
	return content, nil
}

func isGZIP(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
