// Package zipbounds validates ZIP central-directory structure before callers
// invoke archive/zip. The standard library materializes one File object per
// central-directory entry, so checking business limits only after OpenReader
// is too late for attacker-controlled archives containing millions of empty
// entries or giant names.
package zipbounds

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const (
	endOfCentralDirectorySignature = 0x06054b50
	centralDirectorySignature      = 0x02014b50
	endOfCentralDirectoryBytes     = 22
	maxZIPCommentBytes             = 1<<16 - 1
	centralDirectoryHeaderBytes    = 46
)

type Limits struct {
	MaxArchiveBytes          int64
	MaxEntries               uint64
	MaxCentralDirectoryBytes uint64
	MaxNameBytes             uint64
	MaxExtraBytes            uint64
	MaxCommentBytes          uint64
}

// ValidateFile rejects multi-disk and ZIP64 archives. Both formats exceed the
// bounded single-controller import contract and use sentinel counts/offsets
// that would otherwise bypass the classic EOCD limits checked here.
func ValidateFile(path string, limits Limits) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < endOfCentralDirectoryBytes {
		return fmt.Errorf("zip end-of-central-directory record is missing")
	}
	if limits.MaxArchiveBytes > 0 && info.Size() > limits.MaxArchiveBytes {
		return fmt.Errorf("zip is %d bytes, limit is %d", info.Size(), limits.MaxArchiveBytes)
	}

	searchBytes := int64(endOfCentralDirectoryBytes + maxZIPCommentBytes)
	if searchBytes > info.Size() {
		searchBytes = info.Size()
	}
	tail := make([]byte, searchBytes)
	if _, err := file.ReadAt(tail, info.Size()-searchBytes); err != nil && err != io.EOF {
		return err
	}
	eocdOffset := -1
	for index := len(tail) - endOfCentralDirectoryBytes; index >= 0; index-- {
		if binary.LittleEndian.Uint32(tail[index:index+4]) != endOfCentralDirectorySignature {
			continue
		}
		commentBytes := int(binary.LittleEndian.Uint16(tail[index+20 : index+22]))
		if index+endOfCentralDirectoryBytes+commentBytes == len(tail) {
			eocdOffset = index
			break
		}
	}
	if eocdOffset < 0 {
		return fmt.Errorf("zip end-of-central-directory record is invalid")
	}
	eocd := tail[eocdOffset:]
	diskNumber := binary.LittleEndian.Uint16(eocd[4:6])
	centralDisk := binary.LittleEndian.Uint16(eocd[6:8])
	entriesOnDisk := binary.LittleEndian.Uint16(eocd[8:10])
	entryCount16 := binary.LittleEndian.Uint16(eocd[10:12])
	centralBytes32 := binary.LittleEndian.Uint32(eocd[12:16])
	centralOffset32 := binary.LittleEndian.Uint32(eocd[16:20])
	if diskNumber != 0 || centralDisk != 0 || entriesOnDisk != entryCount16 {
		return fmt.Errorf("multi-disk zip archives are not supported")
	}
	if entryCount16 == ^uint16(0) || centralBytes32 == ^uint32(0) || centralOffset32 == ^uint32(0) {
		return fmt.Errorf("ZIP64 archives are not supported")
	}
	entryCount := uint64(entryCount16)
	centralBytes := uint64(centralBytes32)
	centralOffset := uint64(centralOffset32)
	if limits.MaxEntries > 0 && entryCount > limits.MaxEntries {
		return fmt.Errorf("zip contains %d entries, limit is %d", entryCount, limits.MaxEntries)
	}
	if limits.MaxCentralDirectoryBytes > 0 && centralBytes > limits.MaxCentralDirectoryBytes {
		return fmt.Errorf("zip central directory is %d bytes, limit is %d", centralBytes, limits.MaxCentralDirectoryBytes)
	}
	absoluteEOCD := uint64(info.Size()-searchBytes) + uint64(eocdOffset)
	if centralOffset > absoluteEOCD || centralBytes > absoluteEOCD-centralOffset {
		return fmt.Errorf("zip central directory points outside the archive")
	}

	section := io.NewSectionReader(file, int64(centralOffset), int64(centralBytes))
	header := make([]byte, centralDirectoryHeaderBytes)
	var consumed uint64
	for index := uint64(0); index < entryCount; index++ {
		if centralBytes-consumed < centralDirectoryHeaderBytes {
			return fmt.Errorf("zip central directory entry %d is truncated", index)
		}
		if _, err := io.ReadFull(section, header); err != nil {
			return fmt.Errorf("read zip central directory entry %d: %w", index, err)
		}
		consumed += centralDirectoryHeaderBytes
		if binary.LittleEndian.Uint32(header[:4]) != centralDirectorySignature {
			return fmt.Errorf("zip central directory entry %d has an invalid signature", index)
		}
		if binary.LittleEndian.Uint32(header[20:24]) == ^uint32(0) ||
			binary.LittleEndian.Uint32(header[24:28]) == ^uint32(0) ||
			binary.LittleEndian.Uint16(header[34:36]) == ^uint16(0) ||
			binary.LittleEndian.Uint32(header[42:46]) == ^uint32(0) {
			return fmt.Errorf("ZIP64 or multi-disk entry %d is not supported", index)
		}
		nameBytes := uint64(binary.LittleEndian.Uint16(header[28:30]))
		extraBytes := uint64(binary.LittleEndian.Uint16(header[30:32]))
		commentBytes := uint64(binary.LittleEndian.Uint16(header[32:34]))
		if limits.MaxNameBytes > 0 && nameBytes > limits.MaxNameBytes {
			return fmt.Errorf("zip entry %d name is %d bytes, limit is %d", index, nameBytes, limits.MaxNameBytes)
		}
		if limits.MaxExtraBytes > 0 && extraBytes > limits.MaxExtraBytes {
			return fmt.Errorf("zip entry %d extra data is %d bytes, limit is %d", index, extraBytes, limits.MaxExtraBytes)
		}
		if limits.MaxCommentBytes > 0 && commentBytes > limits.MaxCommentBytes {
			return fmt.Errorf("zip entry %d comment is %d bytes, limit is %d", index, commentBytes, limits.MaxCommentBytes)
		}
		variableBytes := nameBytes + extraBytes + commentBytes
		if variableBytes > centralBytes-consumed {
			return fmt.Errorf("zip central directory entry %d exceeds the declared directory", index)
		}
		if _, err := section.Seek(int64(variableBytes), io.SeekCurrent); err != nil {
			return err
		}
		consumed += variableBytes
	}
	if consumed != centralBytes {
		return fmt.Errorf("zip central directory contains undeclared trailing records")
	}
	return nil
}
