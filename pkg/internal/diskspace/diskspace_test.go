package diskspace

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestAvailablePathAndFile(t *testing.T) {
	directory := t.TempDir()
	pathAvailable, err := AvailablePath(directory)
	if err != nil {
		t.Fatalf("AvailablePath() error = %v", err)
	}
	if pathAvailable <= 0 {
		t.Fatalf("AvailablePath() = %d, want positive capacity", pathAvailable)
	}

	file, err := os.CreateTemp(directory, "diskspace-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fileAvailable, err := AvailableFile(file)
	if err != nil {
		t.Fatalf("AvailableFile() error = %v", err)
	}
	if fileAvailable <= 0 {
		t.Fatalf("AvailableFile() = %d, want positive capacity", fileAvailable)
	}

	difference := pathAvailable - fileAvailable
	if difference < 0 {
		difference = -difference
	}
	tolerance := int64(1 << 30)
	if proportional := pathAvailable / 10; proportional > tolerance {
		tolerance = proportional
	}
	if difference > tolerance {
		t.Fatalf("path/file available bytes differ by %d; path=%d file=%d tolerance=%d", difference, pathAvailable, fileAvailable, tolerance)
	}
}

func TestAvailableErrors(t *testing.T) {
	if _, err := AvailablePath(""); err == nil {
		t.Fatal("AvailablePath(\"\") unexpectedly succeeded")
	}
	if _, err := AvailablePath(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("AvailablePath(missing) unexpectedly succeeded")
	}
	if _, err := AvailableFile(nil); err == nil {
		t.Fatal("AvailableFile(nil) unexpectedly succeeded")
	}

	file, err := os.CreateTemp(t.TempDir(), "closed-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := AvailableFile(file); err == nil {
		t.Fatal("AvailableFile(closed) unexpectedly succeeded")
	}
}

func TestAvailableByteCalculationsClampOverflow(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		blocks    uint64
		blockSize uint64
		want      int64
	}{
		{name: "zero", blocks: 0, blockSize: 4096, want: 0},
		{name: "ordinary", blocks: 3, blockSize: 4096, want: 12_288},
		{name: "largest signed value", blocks: uint64(math.MaxInt64), blockSize: 1, want: math.MaxInt64},
		{name: "multiplication overflow", blocks: math.MaxUint64, blockSize: 2, want: math.MaxInt64},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := availableBytesFromBlocks(testCase.blocks, testCase.blockSize); got != testCase.want {
				t.Fatalf("availableBytesFromBlocks(%d, %d) = %d, want %d", testCase.blocks, testCase.blockSize, got, testCase.want)
			}
		})
	}
	if got := clampAvailableBytes(uint64(math.MaxInt64) + 1); got != math.MaxInt64 {
		t.Fatalf("clampAvailableBytes(MaxInt64+1) = %d, want %d", got, int64(math.MaxInt64))
	}
}
