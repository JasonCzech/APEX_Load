package javarunner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"github.com/jasonczech/apexload/pkg/internal/zipbounds"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// NativeInspection is static evidence for explicitly supplied APPLICATION
// artifacts. Do not feed it JDK internals or treat a clean scan as qualification.
// Dynamic loading, reflection, downloads and unobserved runtime work remain
// outside the proof boundary and require representative execution/approval.
type NativeInspection struct {
	Schema      string              `json:"schema"`
	Artifacts   []InspectedArtifact `json:"artifacts"`
	Findings    []NativeFinding     `json:"findings"`
	Complete    bool                `json:"complete"`
	Assessment  string              `json:"assessment"`
	Limitations []string            `json:"limitations"`
}
type InspectedArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type NativeFinding struct {
	Artifact string `json:"artifact_sha256"`
	Entry    string `json:"entry"`
	Kind     string `json:"kind"`
}

type nativeScanner struct {
	ctx     context.Context
	report  NativeInspection
	bytes   int64
	entries int
}

var sourceNativeIndicator = regexp.MustCompile(`\bnative\b|\bloadLibrary\b|\bload\s*\(|com\.sun\.jna|jnr\.ffi`)

const nativeScanBytes = 64 << 20
const nativeScanEntries = 16384

func InspectApplicationNative(ctx context.Context, paths []string) (NativeInspection, error) {
	s := nativeScanner{ctx: ctx, report: NativeInspection{Schema: "apexload.java-native-inspection/v1", Complete: true, Assessment: "no_static_indicators", Limitations: []string{"static_scan_is_not_runtime_qualification", "dynamic_loading_and_background_work_require_representative_execution", "only_explicit_application_artifacts_are_inspected"}}}
	if len(paths) == 0 || len(paths) > 256 {
		return s.incomplete(), errors.New("invalid native inspection artifact count")
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return s.incomplete(), err
		}
		initial, err := os.Lstat(path)
		if err != nil || !initial.Mode().IsRegular() || initial.Size() < 0 || initial.Size() > nativeScanBytes-s.bytes {
			return s.incomplete(), errors.New("native inspection requires bounded regular artifacts")
		}
		file, err := os.Open(path)
		if err != nil {
			return s.incomplete(), errors.New("native inspection artifact unavailable")
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || !os.SameFile(initial, info) || info.Size() != initial.Size() || info.Size() > nativeScanBytes {
			file.Close()
			return s.incomplete(), errors.New("native inspection requires bounded regular artifacts")
		}
		data, readErr := io.ReadAll(&qualificationReader{ctx: ctx, reader: io.LimitReader(file, initial.Size()+1)})
		after, afterErr := file.Stat()
		current, currentErr := os.Lstat(path)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || afterErr != nil || currentErr != nil || !os.SameFile(info, current) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || int64(len(data)) != initial.Size() {
			return s.incomplete(), errors.New("native inspection artifact read failed")
		}
		hash := sha256.Sum256(data)
		artifact := filepath.Base(path)
		s.report.Artifacts = append(s.report.Artifacts, InspectedArtifact{artifact, hex.EncodeToString(hash[:])})
		if err := s.scan(hex.EncodeToString(hash[:]), artifact, data, 0); err != nil {
			return s.incomplete(), err
		}
	}
	if len(s.report.Findings) > 0 {
		s.report.Assessment = "native_indicators_found"
	}
	return s.report, nil
}
func (s *nativeScanner) incomplete() NativeInspection {
	s.report.Complete = false
	s.report.Assessment = "incomplete"
	return s.report
}
func (s *nativeScanner) finding(artifact, entry, kind string) error {
	if len(s.report.Findings) >= 4096 {
		return errors.New("native inspection finding limit exceeded")
	}
	s.report.Findings = append(s.report.Findings, NativeFinding{artifact, entry, kind})
	return nil
}
func (s *nativeScanner) scan(artifact, entry string, data []byte, depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.bytes += int64(len(data))
	s.entries++
	if s.bytes > nativeScanBytes || s.entries > nativeScanEntries || len(entry) > 4096 || depth > 3 {
		return errors.New("native inspection expansion limit exceeded")
	}
	lower := strings.ToLower(entry)
	if strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".so") || strings.Contains(lower, ".so.") || strings.HasSuffix(lower, ".dylib") || strings.HasSuffix(lower, ".jnilib") {
		return s.finding(artifact, entry, "packaged_native_library")
	}
	switch filepath.Ext(lower) {
	case ".jar", ".zip":
		if s.entries >= nativeScanEntries {
			return errors.New("native inspection entry limit exceeded")
		}
		if err := zipbounds.ValidateReader(bytes.NewReader(data), int64(len(data)), zipbounds.Limits{MaxArchiveBytes: nativeScanBytes, MaxEntries: uint64(nativeScanEntries - s.entries), MaxCentralDirectoryBytes: 4 << 20, MaxNameBytes: 4096, MaxExtraBytes: 4096, MaxCommentBytes: 4096}); err != nil {
			return errors.New("invalid or oversized native inspection archive")
		}
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil || len(z.File) > nativeScanEntries-s.entries {
			return errors.New("invalid or oversized native inspection archive")
		}
		for _, f := range z.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if f.UncompressedSize64 > uint64(nativeScanBytes-s.bytes) {
				return errors.New("native inspection expansion limit exceeded")
			}
			r, err := f.Open()
			if err != nil {
				return errors.New("native inspection archive entry unavailable")
			}
			body, readErr := io.ReadAll(&qualificationReader{ctx: s.ctx, reader: io.LimitReader(r, int64(nativeScanBytes)-s.bytes+1)})
			closeErr := r.Close()
			if readErr != nil || closeErr != nil {
				return errors.New("invalid native inspection archive entry")
			}
			if err = s.scan(artifact, entry+"!"+f.Name, body, depth+1); err != nil {
				return err
			}
		}
	case ".java":
		// Conservative token indicators may include comments/strings. Neither
		// obfuscation nor alternate encodings are claimed to be resolved by scans.
		if sourceNativeIndicator.Match(data) {
			return s.finding(artifact, entry, "source_native_indicator")
		}
	case ".class":
		native, load, jna, err := inspectClassNative(data)
		if err != nil {
			return err
		}
		for _, indicator := range []struct {
			kind  string
			found bool
		}{{"native_method", native}, {"bytecode_library_load_indicator", load}, {"native_bridge_dependency", jna}} {
			if indicator.found {
				if err := s.finding(artifact, entry, indicator.kind); err != nil {
					return err
				}
			}
		}
	default:
		if depth == 0 {
			return errors.New("unsupported native inspection artifact type")
		}
	}
	return nil
}

// Parse constant-pool structure and member access flags, rather than matching
// byte substrings that can mistake ordinary UTF-8 constants for native methods.
func inspectClassNative(data []byte) (native, load, jna bool, err error) {
	invalid := errors.New("invalid or unsupported Java class in native inspection")
	r := bytes.NewReader(data)
	u1 := func() (byte, error) { return r.ReadByte() }
	u2 := func() (uint16, error) { var n uint16; e := binary.Read(r, binary.BigEndian, &n); return n, e }
	skip := func(n int64) error {
		if n < 0 || n > int64(r.Len()) {
			return invalid
		}
		_, e := r.Seek(n, io.SeekCurrent)
		return e
	}
	var magic uint32
	if binary.Read(r, binary.BigEndian, &magic) != nil || magic != 0xcafebabe {
		err = invalid
		return
	}
	if skip(4) != nil {
		err = invalid
		return
	}
	count, e := u2()
	if e != nil || count == 0 {
		err = invalid
		return
	}
	for i := 1; i < int(count); i++ {
		tag, e := u1()
		if e != nil {
			err = invalid
			return
		}
		n := int64(0)
		switch tag {
		case 1:
			length, e := u2()
			if e != nil || int(length) > r.Len() {
				err = invalid
				return
			}
			body := make([]byte, int(length))
			if _, e = io.ReadFull(r, body); e != nil {
				err = invalid
				return
			}
			value := string(body)
			load = load || value == "loadLibrary" || value == "load" // evidence only: owner may be unrelated
			jna = jna || strings.Contains(value, "com/sun/jna/") || strings.Contains(value, "jnr/ffi/") || strings.Contains(value, "com.sun.jna.") || strings.Contains(value, "jnr.ffi.")
		case 3, 4, 9, 10, 11, 12, 17, 18:
			n = 4
		case 5, 6:
			n = 8
			i++
			if i >= int(count) {
				err = invalid
				return
			}
		case 7, 8, 16, 19, 20:
			n = 2
		case 15:
			n = 3
		default:
			err = invalid
			return
		}
		if skip(n) != nil {
			err = invalid
			return
		}
	}
	if skip(6) != nil {
		err = invalid
		return
	}
	interfaces, e := u2()
	if e != nil || skip(int64(interfaces)*2) != nil {
		err = invalid
		return
	}
	attributes := func() error {
		count, e := u2()
		if e != nil {
			return invalid
		}
		for i := uint16(0); i < count; i++ {
			if skip(2) != nil {
				return invalid
			}
			var size uint32
			if binary.Read(r, binary.BigEndian, &size) != nil || skip(int64(size)) != nil {
				return invalid
			}
		}
		return nil
	}
	for member := 0; member < 2; member++ {
		count, e := u2()
		if e != nil {
			err = invalid
			return
		}
		for i := uint16(0); i < count; i++ {
			flags, e := u2()
			if e != nil || skip(4) != nil {
				err = invalid
				return
			}
			if member == 1 && flags&0x100 != 0 {
				native = true
			}
			if attributes() != nil {
				err = invalid
				return
			}
		}
	}
	if attributes() != nil || r.Len() != 0 {
		err = invalid
	}
	return
}
