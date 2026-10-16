//go:build !linux

package main

import "os"

// directoryStamp is empty here: these platforms do not hand a deleted
// directory's file identity to its replacement, so os.SameFile suffices.
type directoryStamp struct{}

func directoryStampOf(string, os.FileInfo) directoryStamp { return directoryStamp{} }

func sameDirectoryStamp(directoryStamp, directoryStamp) bool { return true }
