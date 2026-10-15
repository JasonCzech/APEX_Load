package api

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

// Observers should not share users with the load: an observer signed in as a
// load user shares that user's sessions and record locks, so its timings
// measure the load's contention rather than a real user's. Launch preflight
// warns when an observer identity (config.browser.session) also appears in
// another group's load data (its list parameters and data sources), or in a
// second observer group.

const (
	// maxIdentityOverlapScanBytes bounds how much of one data source the
	// check reads.
	maxIdentityOverlapScanBytes = 8 << 20
	// maxIdentityOverlapNamed bounds the users one warning names.
	maxIdentityOverlapNamed = 5
)

type identityOverlap struct {
	observer, other string
	users           []string
	seen            map[string]bool
}

// observerIdentityOverlapLaunchWarnings warns, per observer group and the
// group it shares users with, when observer identities overlap other users.
func observerIdentityOverlapLaunchWarnings(groups []TestGroup) []LaunchConflict {
	type owner struct{ group, username string }
	observers := map[string]owner{}
	overlaps := map[[2]string]*identityOverlap{}
	var order [][2]string
	note := func(observer, other, username string) {
		key := [2]string{observer, other}
		overlap := overlaps[key]
		if overlap == nil {
			overlap = &identityOverlap{observer: observer, other: other, seen: map[string]bool{}}
			overlaps[key] = overlap
			order = append(order, key)
		}
		if lower := strings.ToLower(username); !overlap.seen[lower] {
			overlap.seen[lower] = true
			overlap.users = append(overlap.users, username)
		}
	}
	for _, group := range groups {
		for _, identity := range dsl.BrowserSessionIdentities(group.script) {
			key := strings.ToLower(identity.Username)
			if first, listed := observers[key]; listed {
				if first.group != group.Name {
					note(first.group, group.Name, first.username)
				}
				continue
			}
			observers[key] = owner{group: group.Name, username: identity.Username}
		}
	}
	if len(observers) == 0 {
		return nil
	}
	for _, group := range groups {
		if group.script == nil || len(dsl.BrowserSessionIdentities(group.script)) > 0 {
			continue
		}
		visitScriptDataValues(group.script, func(value string) {
			if first, listed := observers[strings.ToLower(value)]; listed {
				note(first.group, group.Name, first.username)
			}
		})
	}
	warnings := make([]LaunchConflict, 0, len(order))
	for _, key := range order {
		overlap := overlaps[key]
		sort.Strings(overlap.users)
		named := overlap.users
		more := ""
		if extra := len(named) - maxIdentityOverlapNamed; extra > 0 {
			named, more = named[:maxIdentityOverlapNamed], fmt.Sprintf(" and %d more", extra)
		}
		warnings = append(warnings, LaunchConflict{
			Code:       "observer_identity_overlap",
			Severity:   "warning",
			Source:     "browser_session",
			ResourceID: overlap.observer,
			Message: fmt.Sprintf("Observer group %q shares users with group %q: %s%s. Give observers their own users, so their timings don't share sessions or record locks with other users.",
				overlap.observer, overlap.other, strings.Join(named, ", "), more),
		})
	}
	return warnings
}

// visitScriptDataValues visits every value of the script's list parameters
// and every field of its CSV and DAT data sources, reading the first
// maxIdentityOverlapScanBytes of each. Unreadable sources are skipped: the
// check only warns.
func visitScriptDataValues(script *dsl.Script, visit func(string)) {
	for _, parameter := range script.Parameters {
		for _, value := range parameter.Values {
			visit(strings.TrimSpace(value))
		}
	}
	for _, source := range script.DataSources {
		if strings.EqualFold(source.Type, "file") {
			continue
		}
		data := readOverlapDataSource(source)
		if len(data) == 0 {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			for _, field := range strings.FieldsFunc(scanner.Text(), func(r rune) bool {
				return r == ',' || r == '\t' || r == ';' || r == '|'
			}) {
				if value := strings.Trim(strings.TrimSpace(field), `"'`); value != "" {
					visit(value)
				}
			}
		}
	}
}

func readOverlapDataSource(source dsl.DataSource) []byte {
	if inline := source.InlineData; inline != "" {
		if strings.EqualFold(source.Encoding, "base64") {
			decoded, err := base64.StdEncoding.DecodeString(inline)
			if err != nil {
				return nil
			}
			inline = string(decoded)
		}
		if len(inline) > maxIdentityOverlapScanBytes {
			inline = inline[:maxIdentityOverlapScanBytes]
		}
		return []byte(inline)
	}
	var reader io.ReadCloser
	var err error
	switch {
	case strings.TrimSpace(source.StorageRef) != "":
		reader, err = fileasset.OpenStorageRef(strings.TrimSpace(source.StorageRef))
	case strings.TrimSpace(source.Path) != "":
		reader, err = fileasset.OpenPath(strings.TrimSpace(source.Path))
	default:
		return nil
	}
	if err != nil {
		return nil
	}
	defer reader.Close()
	data, _ := io.ReadAll(io.LimitReader(reader, maxIdentityOverlapScanBytes))
	return data
}
