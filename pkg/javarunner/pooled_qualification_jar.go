package javarunner

import (
	"archive/zip"
	"context"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/zipbounds"
)

// URLClassLoader and the system loader can follow manifest Class-Path and JAR
// index references outside the declared classpath. Pilot profiles require an
// explicit dependency set; reject those layouts rather than hash an incomplete
// set of executable assets. This does not prove application code cannot load
// additional code dynamically; that still requires operator qualification.
func validateQualificationJar(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The caller has already checked and hashed this regular immutable file.
	if err := zipbounds.ValidateFile(path, zipbounds.Limits{MaxArchiveBytes: 2 << 30, MaxEntries: 16384, MaxCentralDirectoryBytes: 16 << 20, MaxNameBytes: 4096, MaxExtraBytes: 4096, MaxCommentBytes: 4096}); err != nil {
		return qualificationError(ctx)
	}
	archive, err := zip.OpenReader(path)
	if archive != nil {
		defer archive.Close()
	}
	if err != nil {
		return qualificationError(ctx)
	}
	manifests := 0
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.EqualFold(entry.Name, "META-INF/INDEX.LIST") {
			return ErrPooledQualification
		}
		if !strings.EqualFold(entry.Name, "META-INF/MANIFEST.MF") {
			continue
		}
		manifests++
		if manifests > 1 || entry.UncompressedSize64 > 64<<10 {
			return ErrPooledQualification
		}
		reader, err := entry.Open()
		if err != nil {
			return ErrPooledQualification
		}
		body, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil || len(body) > 64<<10 {
			return ErrPooledQualification
		}
		// Reject the attribute even if empty/folded, in any manifest section. Both
		// CR and LF are line separators in Java's manifest reader.
		for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r", "\n"), "\n") {
			name, _, ok := strings.Cut(strings.TrimSpace(line), ":")
			if ok && strings.EqualFold(strings.TrimSpace(name), "Class-Path") {
				return ErrPooledQualification
			}
		}
	}
	return ctx.Err()
}
