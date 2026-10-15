package archiveingest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io/fs"
	"math"
	"strings"
	"testing"
)

func TestReadZIPValidFilesAndNormalization(t *testing.T) {
	data := zipFixture(t, []zipFixtureEntry{
		{name: `bundle\main.js`, body: "main"},
		{name: "bundle/data.csv", body: "a,b"},
	})
	files, err := ReadZIP(data, Options{})
	if err != nil {
		t.Fatalf("ReadZIP: %v", err)
	}
	if len(files) != 2 || files[0].Name != "bundle/main.js" || string(files[1].Data) != "a,b" {
		t.Fatalf("files = %#v", files)
	}
}

func TestReadZIPStripFirstComponentRequiresCommonArchiveRoot(t *testing.T) {
	t.Run("common wrapper is stripped", func(t *testing.T) {
		files, err := ReadZIP(zipFixture(t, []zipFixtureEntry{
			{name: "bundle/script.usr", body: "usr"},
			{name: "bundle/profiles/a/default.cfg", body: "cfg"},
		}), Options{StripFirstComponent: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 2 || files[0].Name != "script.usr" || files[1].Name != "profiles/a/default.cfg" {
			t.Fatalf("files = %#v, want one common bundle component stripped", files)
		}
	})

	t.Run("mixed root paths are preserved", func(t *testing.T) {
		files, err := ReadZIP(zipFixture(t, []zipFixtureEntry{
			{name: "script.usr", body: "usr"},
			{name: "profiles/a/default.cfg", body: "cfg"},
		}), Options{StripFirstComponent: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 2 || files[0].Name != "script.usr" || files[1].Name != "profiles/a/default.cfg" {
			t.Fatalf("files = %#v, mixed-root entries must retain qualified paths", files)
		}
	})
}

func TestReadZIPRejectsUnsafeMetadataAndNames(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipFixtureEntry
		want    string
	}{
		{name: "traversal", entries: []zipFixtureEntry{{name: "../secret"}}, want: "traversal"},
		{name: "absolute", entries: []zipFixtureEntry{{name: "/tmp/secret"}}, want: "absolute"},
		{name: "drive", entries: []zipFixtureEntry{{name: `C:\secret`}}, want: "drive"},
		{name: "control", entries: []zipFixtureEntry{{name: "bad\x00name"}}, want: "control"},
		{name: "trimmed", entries: []zipFixtureEntry{{name: " bad.txt"}}, want: "whitespace"},
		{name: "dot", entries: []zipFixtureEntry{{name: "./bad.txt"}}, want: "dot"},
		{name: "symlink", entries: []zipFixtureEntry{{name: "link", mode: fs.ModeSymlink | 0o777}}, want: "regular"},
		{name: "case collision", entries: []zipFixtureEntry{{name: "A.txt"}, {name: "a.TXT"}}, want: "colliding"},
		{name: "separator collision", entries: []zipFixtureEntry{{name: `dir\a.txt`}, {name: "dir/a.txt"}}, want: "duplicate"},
		{name: "nfc collision", entries: []zipFixtureEntry{{name: "café.txt"}, {name: "cafe\u0301.txt"}}, want: "colliding"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ReadZIP(zipFixture(t, test.entries), Options{})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("ReadZIP error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadZIPBoundsWork(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		_, err := ReadZIP(zipFixture(t, []zipFixtureEntry{{name: "a"}, {name: "b"}}), Options{
			Limits: Limits{MaxEntries: 1, MaxEntryBytes: 10, MaxTotalBytes: 10, MaxDepth: 2},
		})
		if err == nil || !strings.Contains(err.Error(), "too many entries") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("entry bytes", func(t *testing.T) {
		_, err := ReadZIP(zipFixture(t, []zipFixtureEntry{{name: "a", body: "12345"}}), Options{
			Limits: Limits{MaxEntries: 1, MaxEntryBytes: 4, MaxTotalBytes: 10, MaxDepth: 2},
		})
		if err == nil || !strings.Contains(err.Error(), "maximum") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("total bytes", func(t *testing.T) {
		_, err := ReadZIP(zipFixture(t, []zipFixtureEntry{{name: "a", body: "123"}, {name: "b", body: "456"}}), Options{
			Limits: Limits{MaxEntries: 2, MaxEntryBytes: 4, MaxTotalBytes: 5, MaxDepth: 2},
		})
		if err == nil || !strings.Contains(err.Error(), "total") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		_, err := ReadZIP(zipFixture(t, []zipFixtureEntry{{name: "a/b/c"}}), Options{
			Limits: Limits{MaxEntries: 1, MaxEntryBytes: 4, MaxTotalBytes: 4, MaxDepth: 2},
		})
		if err == nil || !strings.Contains(err.Error(), "depth") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("compression ratio", func(t *testing.T) {
		body := strings.Repeat("A", 64<<10)
		_, err := ReadZIP(zipFixture(t, []zipFixtureEntry{{name: "compressed.txt", body: body}}), Options{
			Limits: Limits{
				MaxEntries:          1,
				MaxEntryBytes:       int64(len(body)),
				MaxTotalBytes:       int64(len(body)),
				MaxDepth:            2,
				MaxCompressionRatio: 4,
			},
		})
		if err == nil || !strings.Contains(err.Error(), "compression-ratio") {
			t.Fatalf("error = %v, want compression-ratio rejection", err)
		}
	})
}

func TestValidateZIPDirectoryCountsBeforeArchiveZipAllocation(t *testing.T) {
	t.Run("actual records defeat forged low EOCD count", func(t *testing.T) {
		data := zipFixture(t, []zipFixtureEntry{{name: "a"}, {name: "b"}})
		end := bytes.LastIndex(data, []byte{'P', 'K', 0x05, 0x06})
		if end < 0 {
			t.Fatal("fixture has no ZIP end record")
		}
		// archive/zip deliberately accepts the 16-bit count modulo 65536 and
		// reads until a non-directory header. A hostile archive can therefore
		// understate this value; the bounded preflight must count actual records.
		binary.LittleEndian.PutUint16(data[end+8:end+10], 0)
		binary.LittleEndian.PutUint16(data[end+10:end+12], 0)

		err := ValidateZIPDirectory(data, 1)
		if err == nil || !strings.Contains(err.Error(), "too many entries") {
			t.Fatalf("ValidateZIPDirectory error = %v, want actual-entry-count rejection", err)
		}
	})

	t.Run("actual records defeat forged low EOCD size and count", func(t *testing.T) {
		data := zipFixture(t, []zipFixtureEntry{{name: "a"}, {name: "b"}})
		end := bytes.LastIndex(data, []byte{'P', 'K', 0x05, 0x06})
		if end < 0 {
			t.Fatal("fixture has no ZIP end record")
		}
		directoryOffset := int(binary.LittleEndian.Uint32(data[end+16 : end+20]))
		if directoryOffset < 0 || len(data)-directoryOffset < zipDirectoryHeaderLength {
			t.Fatal("fixture has no central-directory header")
		}
		firstHeader := data[directoryOffset:]
		firstRecordLength := zipDirectoryHeaderLength +
			int(binary.LittleEndian.Uint16(firstHeader[28:30])) +
			int(binary.LittleEndian.Uint16(firstHeader[30:32])) +
			int(binary.LittleEndian.Uint16(firstHeader[32:34]))

		// archive/zip ignores the declared size while reading headers. Its
		// compatibility base-offset fallback also honors this original
		// directory offset, so it would still allocate both entries.
		binary.LittleEndian.PutUint16(data[end+8:end+10], 1)
		binary.LittleEndian.PutUint16(data[end+10:end+12], 1)
		binary.LittleEndian.PutUint32(data[end+12:end+16], uint32(firstRecordLength))

		err := ValidateZIPDirectory(data, 1)
		if err == nil || !strings.Contains(err.Error(), "too many entries") {
			t.Fatalf("ValidateZIPDirectory error = %v, want understated-size entry-count rejection", err)
		}
	})

	t.Run("self extracting prefix remains compatible", func(t *testing.T) {
		archive := zipFixture(t, []zipFixtureEntry{{name: "bundle/main.js", body: "main"}})
		prefixed := append([]byte("executable-prefix"), archive...)
		files, err := ReadZIP(prefixed, Options{})
		if err != nil {
			t.Fatalf("ReadZIP self-extracting fixture: %v", err)
		}
		if len(files) != 1 || files[0].Name != "bundle/main.js" {
			t.Fatalf("files = %#v", files)
		}
	})

	t.Run("ZIP64 declared count is bounded before parsing", func(t *testing.T) {
		data := emptyZIP64Fixture(100_000)
		err := ValidateZIPDirectory(data, 4_096)
		if err == nil || !strings.Contains(err.Error(), "100000 > maximum 4096") {
			t.Fatalf("ValidateZIPDirectory error = %v, want ZIP64 count rejection", err)
		}
	})
}

func TestReadZIPDirectoryMetadataDoesNotTreatExactUint16SizeAsZIP64(t *testing.T) {
	// Only the 32-bit all-ones value is the EOCD ZIP64 sentinel for directory
	// size. A perfectly ordinary central directory can be exactly 65,535 bytes;
	// treating math.MaxUint16 as a sentinel rejects that archive as missing a
	// ZIP64 locator before archive/zip can parse it.
	data := make([]byte, int(math.MaxUint16)+zipDirectoryEndLength)
	end := len(data) - zipDirectoryEndLength
	binary.LittleEndian.PutUint32(data[end:end+4], zipDirectoryEndSignature)
	binary.LittleEndian.PutUint32(data[end+12:end+16], math.MaxUint16)

	metadata, err := readZIPDirectoryMetadata(data)
	if err != nil {
		t.Fatalf("readZIPDirectoryMetadata returned error: %v", err)
	}
	if metadata.size != math.MaxUint16 {
		t.Fatalf("directory size = %d, want %d", metadata.size, uint64(math.MaxUint16))
	}
}

func TestExceedsZIPCompressionRatioBoundaries(t *testing.T) {
	tests := []struct {
		name                 string
		expanded, compressed uint64
		maximum              int64
		want                 bool
	}{
		{name: "empty", expanded: 0, compressed: 0, maximum: 10},
		{name: "zero compressed nonempty", expanded: 1, compressed: 0, maximum: 10, want: true},
		{name: "exact boundary", expanded: 100, compressed: 10, maximum: 10},
		{name: "fraction over boundary", expanded: 101, compressed: 10, maximum: 10, want: true},
		{name: "large compressed cannot overflow", expanded: ^uint64(0), compressed: ^uint64(0), maximum: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exceedsZIPCompressionRatio(tt.expanded, tt.compressed, tt.maximum); got != tt.want {
				t.Fatalf(
					"exceedsZIPCompressionRatio(%d, %d, %d) = %t, want %t",
					tt.expanded,
					tt.compressed,
					tt.maximum,
					got,
					tt.want,
				)
			}
		})
	}
}

func TestReadTARRejectsLinksAndPreservesRegularFiles(t *testing.T) {
	var valid bytes.Buffer
	tw := tar.NewWriter(&valid)
	if err := tw.WriteHeader(&tar.Header{Name: "root/main.js", Typeflag: tar.TypeReg, Mode: 0o600, Size: 4}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("main"))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := ReadTAR(bytes.NewReader(valid.Bytes()), Options{})
	if err != nil || len(files) != 1 || files[0].Name != "root/main.js" {
		t.Fatalf("files=%#v err=%v", files, err)
	}

	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo} {
		t.Run(fmt.Sprintf("type_%d", kind), func(t *testing.T) {
			var archive bytes.Buffer
			writer := tar.NewWriter(&archive)
			if err := writer.WriteHeader(&tar.Header{Name: "unsafe", Typeflag: kind, Linkname: "target"}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			_, err := ReadTAR(bytes.NewReader(archive.Bytes()), Options{})
			if err == nil || !strings.Contains(err.Error(), "regular files") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReadTARBoundsHiddenExtendedHeaderWork(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	const paxRecord = "20 path=ignored.txt\n"
	var extendedHeaderOffsets []int
	for index := 0; index < 100; index++ {
		extendedHeaderOffsets = append(
			extendedHeaderOffsets,
			(archive.Len()+511)/512*512,
		)
		if err := writer.WriteHeader(&tar.Header{
			Name:     fmt.Sprintf("pax-%d", index),
			Typeflag: tar.TypeReg,
			Mode:     0o600,
			Size:     int64(len(paxRecord)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(paxRecord)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.WriteHeader(&tar.Header{Name: "main", Typeflag: tar.TypeReg, Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := archive.Bytes()
	for _, offset := range extendedHeaderOffsets {
		setTARTypeFlag(t, data[offset:offset+512], tar.TypeXHeader)
	}

	_, err := ReadTAR(bytes.NewReader(data), Options{Limits: Limits{
		MaxEntries: 1, MaxEntryBytes: 1, MaxTotalBytes: 1, MaxDepth: 1,
	}})
	if err == nil || !strings.Contains(err.Error(), "maximum TAR input size") {
		t.Fatalf("ReadTAR error = %v, want hidden metadata input-limit rejection", err)
	}
}

func setTARTypeFlag(t *testing.T, header []byte, typeFlag byte) {
	t.Helper()
	if len(header) != 512 {
		t.Fatalf("TAR header length = %d, want 512", len(header))
	}
	header[156] = typeFlag
	for index := 148; index < 156; index++ {
		header[index] = ' '
	}
	var checksum int
	for _, value := range header {
		checksum += int(value)
	}
	copy(header[148:156], []byte(fmt.Sprintf("%06o\x00 ", checksum)))
}

func FuzzReadZIPBounded(f *testing.F) {
	f.Add(zipFixture(f, []zipFixtureEntry{{name: "main.js", body: "ok"}}))
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		files, _ := ReadZIP(data, Options{Limits: Limits{
			MaxEntries: 16, MaxEntryBytes: 1024, MaxTotalBytes: 4096, MaxDepth: 8,
		}})
		var total int
		for _, file := range files {
			total += len(file.Data)
		}
		if len(files) > 16 || total > 4096 {
			t.Fatalf("limits escaped: files=%d bytes=%d", len(files), total)
		}
	})
}

type testingTB interface {
	Helper()
	Fatalf(string, ...any)
}

type zipFixtureEntry struct {
	name string
	body string
	mode fs.FileMode
}

func zipFixture(t testingTB, entries []zipFixtureEntry) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("CreateHeader: %v", err)
		}
		if _, err := file.Write([]byte(entry.body)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return archive.Bytes()
}

func emptyZIP64Fixture(records uint64) []byte {
	data := make([]byte, zipDirectory64EndLength+zipDirectory64LocatorLength+zipDirectoryEndLength)
	binary.LittleEndian.PutUint32(data[0:4], zipDirectory64EndSignature)
	binary.LittleEndian.PutUint64(data[4:12], zipDirectory64EndLength-12)
	binary.LittleEndian.PutUint16(data[12:14], 45)
	binary.LittleEndian.PutUint16(data[14:16], 45)
	binary.LittleEndian.PutUint64(data[24:32], records)
	binary.LittleEndian.PutUint64(data[32:40], records)

	locator := zipDirectory64EndLength
	binary.LittleEndian.PutUint32(data[locator:locator+4], zipDirectory64LocatorSignature)
	binary.LittleEndian.PutUint64(data[locator+8:locator+16], 0)
	binary.LittleEndian.PutUint32(data[locator+16:locator+20], 1)

	end := locator + zipDirectory64LocatorLength
	binary.LittleEndian.PutUint32(data[end:end+4], zipDirectoryEndSignature)
	binary.LittleEndian.PutUint16(data[end+8:end+10], math.MaxUint16)
	binary.LittleEndian.PutUint16(data[end+10:end+12], math.MaxUint16)
	binary.LittleEndian.PutUint32(data[end+12:end+16], math.MaxUint32)
	binary.LittleEndian.PutUint32(data[end+16:end+20], math.MaxUint32)
	return data
}

// Operating-system and version-control files are not content: a ZIP from a
// Mac (a __MACOSX folder of ._ resource forks, .DS_Store) or a working copy
// (.git) yields only the project files, with its container stripped.
func TestReadZIPLeavesOutOperatingSystemAndVersionControlFiles(t *testing.T) {
	files, err := ReadZIP(zipFixture(t, []zipFixtureEntry{
		{name: "Script/Script.usr", body: "usr"},
		{name: "Script/Action.c", body: "Action() { return 0; }"},
		{name: "Script/.DS_Store", body: "Bud1"},
		{name: "__MACOSX/Script/._Action.c", body: "appledouble"},
		{name: "Script/.git/HEAD", body: "ref: refs/heads/main"},
		{name: "Script/.gitignore", body: "*.log"},
	}), Options{StripFirstComponent: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "Script.usr" || files[1].Name != "Action.c" {
		t.Fatalf("files = %#v, want the two project files with the Script container stripped", files)
	}
	files, err = ReadZIP(zipFixture(t, []zipFixtureEntry{
		{name: "main.js", body: "main"},
		{name: "._main.js", body: "appledouble"},
	}), Options{})
	if err != nil || len(files) != 1 || files[0].Name != "main.js" {
		t.Fatalf("files=%#v err=%v", files, err)
	}
}

func TestReadTARLeavesOutOperatingSystemAndVersionControlFiles(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, entry := range []struct{ name, body string }{
		{"root/main.js", "main"},
		{"root/.DS_Store", "Bud1"},
		{"root/.git/config", "[core]"},
	} {
		if err := writer.WriteHeader(&tar.Header{Name: entry.name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write([]byte(entry.body))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := ReadTAR(bytes.NewReader(archive.Bytes()), Options{})
	if err != nil || len(files) != 1 || files[0].Name != "root/main.js" || string(files[0].Data) != "main" {
		t.Fatalf("files=%#v err=%v", files, err)
	}
}
