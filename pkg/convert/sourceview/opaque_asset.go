package sourceview

import (
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"strings"
)

// IsOpaqueAsset prevents executable, data-store, and other binary bytes from
// becoming source text. Their paths and fingerprints belong in asset evidence.
func IsOpaqueAsset(name, content string) bool {
	switch strings.ToLower(archiveingest.LogicalExt(name)) {
	case ".jar", ".class", ".zip", ".usz", ".tar", ".gz", ".7z", ".rar", ".exe", ".dll", ".so", ".dylib", ".bin", ".o", ".obj", ".lib", ".a", ".store", ".jks", ".keystore", ".p12", ".pfx", ".der", ".pem", ".crt", ".cer", ".p7b", ".p7c", ".key", ".db", ".sqlite", ".sqlite3", ".mdb", ".ico", ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".woff", ".woff2", ".ttf", ".otf":
		return true
	}
	for _, magic := range []string{"PK\x03\x04", "PK\x05\x06", "PK\x07\x08", "\xca\xfe\xba\xbe", "\x7fELF", "SQLite format 3\x00", "\x89PNG\r\n\x1a\n", "GIF87a", "GIF89a", "%PDF-"} {
		if strings.HasPrefix(content, magic) {
			return true
		}
	}
	if len(content) >= 64 && strings.HasPrefix(content, "MZ") {
		return true
	}
	if strings.ContainsRune(content[:min(len(content), 512)], 0) {
		_, recognizedText := decodeReviewSourceText(name, content)
		return !recognizedText
	}
	return false
}
