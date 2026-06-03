// Package refkey produces and parses human-readable, environment-aware
// reference keys for APEX Load artifacts (scripts, scenarios, runs).
//
// Reference keys live alongside the opaque UUID primary keys. They are not
// unique by construction here — uniqueness is guaranteed by the monotonic
// sequence counters maintained by the caller (and a unique index in storage).
// This package is pure: it only formats and parses strings, performing no I/O.
//
// Formats:
//
//	Script   <ENV>-SCR-<seq4>-v<N>     e.g. LOC-SCR-0042-v3
//	Scenario <ENV>-SCN-<seq4>          e.g. LOC-SCN-0007
//	Run      <ENV>-RUN-<YYYYMMDD>-<seq3>  e.g. LOC-RUN-20260603-001
package refkey

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Artifact type segments.
const (
	TypeScript   = "SCR"
	TypeScenario = "SCN"
	TypeRun      = "RUN"
)

// runDateLayout is the calendar-day segment used in run keys.
const runDateLayout = "20060102"

// knownEnvCodes maps the long environment names produced by
// buildinfo.Environment() to their short uppercase codes.
var knownEnvCodes = map[string]string{
	"local":      "LOC",
	"docker":     "DOC",
	"kubernetes": "KUB",
}

// EnvCode converts an environment name into a short, uppercase, alphanumeric
// code suitable for a reference-key segment. Known names map to fixed codes;
// custom names (e.g. a custom APEXLOAD_ENVIRONMENT like "staging") are
// sanitized to [A-Z0-9] and truncated to 4 characters. An empty or
// unsanitizable input falls back to "ENV".
func EnvCode(env string) string {
	trimmed := strings.ToLower(strings.TrimSpace(env))
	if code, ok := knownEnvCodes[trimmed]; ok {
		return code
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(trimmed) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() == 4 {
			break
		}
	}
	if b.Len() == 0 {
		return "ENV"
	}
	return b.String()
}

// Script formats a script reference key. seq is the per-environment script
// sequence; version is the script version shown as the -vN suffix.
func Script(env string, seq, version int) string {
	if version < 1 {
		version = 1
	}
	return fmt.Sprintf("%s-%s-%04d-v%d", EnvCode(env), TypeScript, seq, version)
}

// Scenario formats a scenario reference key.
func Scenario(env string, seq int) string {
	return fmt.Sprintf("%s-%s-%04d", EnvCode(env), TypeScenario, seq)
}

// Run formats a run/result reference key. The sequence resets per calendar
// day, so day determines the date segment and seq is that day's counter.
func Run(env string, day time.Time, seq int) string {
	return fmt.Sprintf("%s-%s-%s-%03d", EnvCode(env), TypeRun, day.Format(runDateLayout), seq)
}

// Parts is the decomposed form of a reference key returned by Parse.
type Parts struct {
	Env  string // environment code segment, e.g. "LOC"
	Type string // one of TypeScript, TypeScenario, TypeRun
	Seq  int    // sequence number
	// Version is the script version (>=1) for script keys; 0 otherwise.
	Version int
	// Date is the calendar day for run keys; zero value otherwise.
	Date time.Time
}

// Parse decomposes a reference key. It returns ok=false for any input that is
// not a well-formed key. Parse is the inverse of Script/Scenario/Run.
func Parse(key string) (Parts, bool) {
	segs := strings.Split(strings.TrimSpace(key), "-")
	if len(segs) < 3 {
		return Parts{}, false
	}
	env := segs[0]
	if env == "" {
		return Parts{}, false
	}
	env = strings.ToUpper(env)
	switch strings.ToUpper(segs[1]) {
	case TypeScript:
		// <ENV>-SCR-<seq>-v<N>
		if len(segs) != 4 {
			return Parts{}, false
		}
		versionSeg := strings.TrimSpace(segs[3])
		if !strings.HasPrefix(strings.ToLower(versionSeg), "v") {
			return Parts{}, false
		}
		seq, err := strconv.Atoi(segs[2])
		if err != nil {
			return Parts{}, false
		}
		version, err := strconv.Atoi(versionSeg[1:])
		if err != nil || version < 1 {
			return Parts{}, false
		}
		return Parts{Env: env, Type: TypeScript, Seq: seq, Version: version}, true
	case TypeScenario:
		// <ENV>-SCN-<seq>
		if len(segs) != 3 {
			return Parts{}, false
		}
		seq, err := strconv.Atoi(segs[2])
		if err != nil {
			return Parts{}, false
		}
		return Parts{Env: env, Type: TypeScenario, Seq: seq}, true
	case TypeRun:
		// <ENV>-RUN-<YYYYMMDD>-<seq>
		if len(segs) != 4 {
			return Parts{}, false
		}
		day, err := time.Parse(runDateLayout, segs[2])
		if err != nil {
			return Parts{}, false
		}
		seq, err := strconv.Atoi(segs[3])
		if err != nil {
			return Parts{}, false
		}
		return Parts{Env: env, Type: TypeRun, Seq: seq, Date: day}, true
	default:
		return Parts{}, false
	}
}

// Looks reports whether s has the shape of a reference key (parses cleanly).
// Useful for handlers that accept either a UUID or a reference key.
func Looks(s string) bool {
	_, ok := Parse(s)
	return ok
}
