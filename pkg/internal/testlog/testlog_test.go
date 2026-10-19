package testlog

import (
	"bytes"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func TestSetDefaultRestoresSlogAndTheLogPackage(t *testing.T) {
	var outside bytes.Buffer
	previousWriter, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(&outside)
	log.SetFlags(log.Lmicroseconds)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})
	original := slog.Default()

	var captured bytes.Buffer
	t.Run("capture", func(t *testing.T) {
		SetDefault(t, slog.New(slog.NewTextHandler(&captured, nil)))
		slog.Info("inside the capture")
		log.Print("log package inside the capture")
	})

	if !strings.Contains(captured.String(), "inside the capture") || !strings.Contains(captured.String(), "log package inside the capture") {
		t.Fatalf("captured logs = %q, want both lines", captured.String())
	}
	if slog.Default() != original {
		t.Fatal("slog default was not restored")
	}
	if log.Writer() != &outside || log.Flags() != log.Lmicroseconds {
		t.Fatalf("log package writer/flags = %v/%d, want the test writer and %d", log.Writer(), log.Flags(), log.Lmicroseconds)
	}
	before := captured.Len()
	slog.Info("after the capture")
	log.Print("log package after the capture")
	if captured.Len() != before {
		t.Fatalf("logs after the capture still reached its handler: %q", captured.String()[before:])
	}
	if !strings.Contains(outside.String(), "after the capture") || !strings.Contains(outside.String(), "log package after the capture") {
		t.Fatalf("logs after the capture = %q, want both lines on the restored writer", outside.String())
	}
}
