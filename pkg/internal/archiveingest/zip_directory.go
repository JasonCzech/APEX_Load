package archiveingest

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	zipDirectoryHeaderSignature    = uint32(0x02014b50)
	zipDirectoryEndSignature       = uint32(0x06054b50)
	zipDirectory64LocatorSignature = uint32(0x07064b50)
	zipDirectory64EndSignature     = uint32(0x06064b50)
	zipDirectoryDigitalSignature   = uint32(0x05054b50)
	zipArchiveExtraDataSignature   = uint32(0x08064b50)

	zipDirectoryHeaderLength    = 46
	zipDirectoryEndLength       = 22
	zipDirectory64LocatorLength = 20
	zipDirectory64EndLength     = 56
	zipMaximumCommentLength     = 1<<16 - 1
)

type zipDirectoryMetadata struct {
	endOffset int64
	records   uint64
	size      uint64
	offset    uint64
}

// ValidateZIPDirectory bounds central-directory work before archive/zip builds
// its []*zip.File slice. Checking len(Reader.File) after zip.NewReader is too
// late for an entry-flood archive: a bounded compressed upload can otherwise
// force hundreds of thousands of metadata allocations before being rejected.
//
// The scan uses both the EOCD/ZIP64 declared count and the actual sequence of
// central-directory records, so a forged low 16-bit EOCD count cannot bypass
// maxEntries. archive/zip remains the authoritative full-format parser after
// this allocation-safe preflight.
func ValidateZIPDirectory(data []byte, maxEntries int) error {
	if maxEntries < 1 {
		return errors.New("maximum ZIP entry count must be positive")
	}
	metadata, err := readZIPDirectoryMetadata(data)
	if err != nil {
		return fmt.Errorf("inspect ZIP central directory: %w", err)
	}
	if metadata.records > uint64(maxEntries) {
		return limitErrorf("archive has too many entries (%d > maximum %d)", metadata.records, maxEntries)
	}

	start, _, err := zipDirectoryBounds(data, metadata)
	if err != nil {
		return fmt.Errorf("inspect ZIP central directory: %w", err)
	}
	// archive/zip deliberately reads file headers until a non-directory header;
	// it does not stop at the EOCD's declared directory size. Scan through the
	// physical directory end for the same reason. Otherwise an attacker can
	// understate both the count and size, trigger archive/zip's base-offset
	// compatibility fallback, and make it allocate every undeclared header
	// after this preflight has counted only the first one.
	scanEnd := int(metadata.endOffset)
	if scanEnd < start || scanEnd > len(data) {
		return errors.New("inspect ZIP central directory: physical directory bounds are invalid")
	}
	count, err := countZIPDirectoryRecords(data[start:scanEnd], maxEntries)
	if err != nil {
		return fmt.Errorf("inspect ZIP central directory: %w", err)
	}
	if count > maxEntries {
		return limitErrorf("archive has too many entries (%d > maximum %d)", count, maxEntries)
	}
	return nil
}

func readZIPDirectoryMetadata(data []byte) (zipDirectoryMetadata, error) {
	endOffset, err := findZIPDirectoryEnd(data)
	if err != nil {
		return zipDirectoryMetadata{}, err
	}
	end := data[endOffset:]
	metadata := zipDirectoryMetadata{
		endOffset: int64(endOffset),
		records:   uint64(binary.LittleEndian.Uint16(end[10:12])),
		size:      uint64(binary.LittleEndian.Uint32(end[12:16])),
		offset:    uint64(binary.LittleEndian.Uint32(end[16:20])),
	}

	if metadata.records != math.MaxUint16 &&
		metadata.size != math.MaxUint32 &&
		metadata.offset != math.MaxUint32 {
		return metadata, nil
	}
	if endOffset < zipDirectory64LocatorLength {
		return zipDirectoryMetadata{}, errors.New("ZIP64 end locator is missing")
	}
	locator := data[endOffset-zipDirectory64LocatorLength : endOffset]
	if binary.LittleEndian.Uint32(locator[:4]) != zipDirectory64LocatorSignature {
		return zipDirectoryMetadata{}, errors.New("ZIP64 end locator is missing")
	}
	if binary.LittleEndian.Uint32(locator[4:8]) != 0 ||
		binary.LittleEndian.Uint32(locator[16:20]) != 1 {
		return zipDirectoryMetadata{}, errors.New("multi-disk ZIP archives are not supported")
	}
	recordOffset := binary.LittleEndian.Uint64(locator[8:16])
	if recordOffset > uint64(len(data)) ||
		uint64(len(data))-recordOffset < zipDirectory64EndLength {
		return zipDirectoryMetadata{}, errors.New("ZIP64 end record is outside the archive")
	}
	record := data[recordOffset:]
	if binary.LittleEndian.Uint32(record[:4]) != zipDirectory64EndSignature {
		return zipDirectoryMetadata{}, errors.New("ZIP64 end record is invalid")
	}
	metadata.endOffset = int64(recordOffset)
	metadata.records = binary.LittleEndian.Uint64(record[32:40])
	metadata.size = binary.LittleEndian.Uint64(record[40:48])
	metadata.offset = binary.LittleEndian.Uint64(record[48:56])
	return metadata, nil
}

func findZIPDirectoryEnd(data []byte) (int, error) {
	if len(data) < zipDirectoryEndLength {
		return 0, errors.New("ZIP end record is missing")
	}
	start := len(data) - zipDirectoryEndLength - zipMaximumCommentLength
	if start < 0 {
		start = 0
	}
	for offset := len(data) - zipDirectoryEndLength; offset >= start; offset-- {
		if binary.LittleEndian.Uint32(data[offset:offset+4]) != zipDirectoryEndSignature {
			continue
		}
		commentLength := int(binary.LittleEndian.Uint16(data[offset+20 : offset+22]))
		if offset+zipDirectoryEndLength+commentLength <= len(data) {
			return offset, nil
		}
	}
	return 0, errors.New("ZIP end record is missing or truncated")
}

func zipDirectoryBounds(data []byte, metadata zipDirectoryMetadata) (int, int, error) {
	if metadata.size > math.MaxInt64 || metadata.offset > math.MaxInt64 {
		return 0, 0, errors.New("ZIP central directory bounds overflow")
	}
	baseOffset := metadata.endOffset - int64(metadata.size) - int64(metadata.offset)
	if baseOffset < 0 {
		return 0, 0, errors.New("ZIP central directory offset is invalid")
	}

	// Match archive/zip's compatibility behavior for archives whose end record
	// implies a prepended-data base but whose directory offset is already an
	// absolute file offset.
	if baseOffset > 0 && metadata.offset <= uint64(len(data)-4) {
		direct := int(metadata.offset)
		if direct <= len(data)-4 &&
			binary.LittleEndian.Uint32(data[direct:direct+4]) == zipDirectoryHeaderSignature {
			baseOffset = 0
		}
	}

	start64 := uint64(baseOffset) + metadata.offset
	if start64 > uint64(len(data)) || metadata.size > uint64(len(data))-start64 {
		return 0, 0, errors.New("ZIP central directory lies outside the archive")
	}
	end64 := start64 + metadata.size
	if end64 > uint64(metadata.endOffset) {
		return 0, 0, errors.New("ZIP central directory overlaps its end record")
	}
	return int(start64), int(end64), nil
}

func countZIPDirectoryRecords(directory []byte, maximum int) (int, error) {
	count := 0
	for offset := 0; offset < len(directory); {
		if len(directory)-offset < 4 {
			return 0, errors.New("truncated ZIP central-directory record")
		}
		switch signature := binary.LittleEndian.Uint32(directory[offset : offset+4]); signature {
		case zipDirectoryHeaderSignature:
			if len(directory)-offset < zipDirectoryHeaderLength {
				return 0, errors.New("truncated ZIP central-directory file header")
			}
			nameLength := int(binary.LittleEndian.Uint16(directory[offset+28 : offset+30]))
			extraLength := int(binary.LittleEndian.Uint16(directory[offset+30 : offset+32]))
			commentLength := int(binary.LittleEndian.Uint16(directory[offset+32 : offset+34]))
			recordLength := zipDirectoryHeaderLength + nameLength + extraLength + commentLength
			if recordLength > len(directory)-offset {
				return 0, errors.New("truncated ZIP central-directory file metadata")
			}
			count++
			if count > maximum {
				return count, nil
			}
			offset += recordLength
		case zipDirectoryDigitalSignature:
			if len(directory)-offset < 6 {
				return 0, errors.New("truncated ZIP central-directory signature")
			}
			recordLength := 6 + int(binary.LittleEndian.Uint16(directory[offset+4:offset+6]))
			if recordLength > len(directory)-offset {
				return 0, errors.New("truncated ZIP central-directory signature data")
			}
			offset += recordLength
		case zipArchiveExtraDataSignature:
			if len(directory)-offset < 8 {
				return 0, errors.New("truncated ZIP archive extra-data record")
			}
			extraLength := binary.LittleEndian.Uint32(directory[offset+4 : offset+8])
			if uint64(extraLength) > uint64(len(directory)-offset-8) {
				return 0, errors.New("truncated ZIP archive extra-data payload")
			}
			offset += 8 + int(extraLength)
		default:
			return 0, fmt.Errorf("unexpected ZIP central-directory signature 0x%08x", signature)
		}
	}
	return count, nil
}
