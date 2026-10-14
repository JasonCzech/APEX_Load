package bruno

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// A header whose bytes HTTP cannot carry, such as a cookie holding a pasted
// curl fragment, used to pass import and then stop the first debug run.
// Inspection now reports it with a fix the operator chooses at commit.
const (
	NativeHeaderRepairDiagnosticCode = "bruno_invalid_header_value"
	HeaderRepairTrim                 = "trim"
	HeaderRepairRemove               = "remove"

	// Very long values are offered removal only.
	maxTrimmableHeaderValueBytes = 8 << 10
	// Cuts checked one by one when the binary search finds no valid prefix.
	maxHeaderTrimFallbackChecks = 64
)

// NativeHeaderRepair locates one invalid request header and describes the
// fixes available for it. Only a left-out header carries a value: the valid
// text trim puts back.
type NativeHeaderRepair struct {
	ActionIndex       int      `json:"action_index"`
	HeaderIndex       int      `json:"header_index"`
	HeaderName        string   `json:"header_name"`
	Options           []string `json:"options"`
	KeptCharacters    int      `json:"kept_characters,omitempty"`
	RemovedCharacters int      `json:"removed_characters,omitempty"`
	InvalidCharacter  string   `json:"invalid_character,omitempty"`
	// Omitted is set for an imported header that is not a credential: it was
	// left out of the candidate, HeaderIndex is where it would go back, and
	// TrimmedValue is what trim puts back. Its value is not a secret.
	Omitted      bool   `json:"omitted,omitempty"`
	TrimmedValue string `json:"trimmed_value,omitempty"`
	// ReplacesBodyDefault is set when a left-out Content-Type was replaced by
	// the request body's default, at HeaderIndex: trim then overwrites that
	// default and remove keeps it.
	ReplacesBodyDefault bool `json:"replaces_body_default,omitempty"`
}

// NativeHeaderRepairDiagnostics reports each top-level request header in
// script whose name or value HTTP does not allow. The script must hold the
// values that will be saved, with imported credentials already restored.
func NativeHeaderRepairDiagnostics(candidateID, sourcePath string, script *dsl.Script) []NativeDiagnostic {
	if script == nil {
		return nil
	}
	inherited := dsl.InheritedHTTPVersion(script)
	var diagnostics []NativeDiagnostic
	for actionIndex, action := range script.Actions {
		version, err := dsl.EffectiveHTTPVersion(action, inherited)
		if err != nil {
			version = "2"
		}
		for headerIndex, header := range action.HeaderList {
			if header.Enabled != nil && !*header.Enabled {
				continue
			}
			code, invalid := headerSyntaxIssue(header.Name, header.Value, version)
			if !invalid {
				continue
			}
			repair := NativeHeaderRepair{
				ActionIndex: actionIndex,
				HeaderIndex: headerIndex,
				HeaderName:  header.Name,
				Options:     []string{HeaderRepairRemove},
			}
			message := "A request header name contains characters HTTP does not allow, so the request cannot be sent as imported. Remove the header, or acknowledge it and fix the name in the editor."
			if code == httpprotocol.HeaderListIssueInvalidValue {
				detail := httpprotocol.InvalidHeaderValueDetail(header.Name, header.Value)
				message = fmt.Sprintf("The %s %s, so the request cannot be sent as imported.", header.Name, detail)
				if kept, ok := trimmedHeaderValue(header.Name, header.Value, version); ok {
					repair.Options = []string{HeaderRepairTrim, HeaderRepairRemove}
					repair.KeptCharacters = utf8.RuneCountInString(kept)
					repair.RemovedCharacters = utf8.RuneCountInString(header.Value) - repair.KeptCharacters
					repair.InvalidCharacter = describeHeaderCharacter(header.Value[len(kept):])
					message = fmt.Sprintf(
						"The %s %s, starting at %s, so the request cannot be sent as imported.",
						header.Name, detail, repair.InvalidCharacter,
					)
				}
			} else {
				// An invalid name is not echoed back, matching the transport's
				// disclosure-safe issue detail.
				repair.HeaderName = ""
			}
			seed := strings.Join([]string{
				"bruno-native-header-repair", candidateID,
				strconv.Itoa(actionIndex), strconv.Itoa(headerIndex),
			}, "\x00")
			hash := sha256.Sum256([]byte(seed))
			id := "bruno-native-" + hex.EncodeToString(hash[:12])
			repairCopy := repair
			diagnostics = append(diagnostics, NativeDiagnostic{
				ID:                      id,
				FindingID:               id,
				Severity:                "warning",
				Code:                    NativeHeaderRepairDiagnosticCode,
				Path:                    sourcePath,
				Message:                 message,
				AcknowledgementRequired: true,
				HeaderRepair:            &repairCopy,
			})
		}
	}
	return diagnostics
}

// separateUnsendableHeaders takes the headers HTTP cannot carry out of a
// candidate's literal header list before the shared request pipeline sees
// them, which would otherwise drop them silently and block replay. Each
// becomes a review finding: trim puts the valid leading text back at commit,
// remove leaves the header out. Credential headers hold secret placeholders
// here and are checked later, once their values are restored.
func separateUnsendableHeaders(headers dsl.HeaderList, identity, sourcePath string) (dsl.HeaderList, []NativeDiagnostic) {
	kept := make(dsl.HeaderList, 0, len(headers))
	var diagnostics []NativeDiagnostic
	for index, header := range headers {
		if header.Enabled != nil && !*header.Enabled {
			kept = append(kept, header)
			continue
		}
		code, invalid := headerSyntaxIssue(header.Name, header.Value, "")
		if !invalid {
			kept = append(kept, header)
			continue
		}
		repair := NativeHeaderRepair{
			HeaderIndex: len(kept),
			HeaderName:  header.Name,
			Options:     []string{HeaderRepairRemove},
			Omitted:     true,
		}
		message := "A request header name contains characters HTTP does not allow, so the header was left out of the script."
		if code == httpprotocol.HeaderListIssueInvalidValue {
			detail := httpprotocol.InvalidHeaderValueDetail(header.Name, header.Value)
			message = fmt.Sprintf("The %s %s, so the header was left out of the script.", header.Name, detail)
			if trimmed, ok := trimmedHeaderValue(header.Name, header.Value, ""); ok {
				repair.Options = []string{HeaderRepairTrim, HeaderRepairRemove}
				repair.TrimmedValue = trimmed
				repair.KeptCharacters = utf8.RuneCountInString(trimmed)
				repair.RemovedCharacters = utf8.RuneCountInString(header.Value) - repair.KeptCharacters
				repair.InvalidCharacter = describeHeaderCharacter(header.Value[len(trimmed):])
				message = fmt.Sprintf(
					"The %s %s, starting at %s, so the header was left out of the script.",
					header.Name, detail, repair.InvalidCharacter,
				)
			}
		} else {
			repair.HeaderName = ""
		}
		seed := strings.Join([]string{"bruno-native-omitted-header", identity, strconv.Itoa(index)}, "\x00")
		hash := sha256.Sum256([]byte(seed))
		id := "bruno-native-" + hex.EncodeToString(hash[:12])
		repairCopy := repair
		diagnostics = append(diagnostics, NativeDiagnostic{
			ID:                      id,
			FindingID:               id,
			Severity:                "warning",
			Code:                    NativeHeaderRepairDiagnosticCode,
			Path:                    sourcePath,
			Message:                 message,
			AcknowledgementRequired: true,
			HeaderRepair:            &repairCopy,
		})
	}
	return kept, diagnostics
}

// ApplyNativeHeaderRepair applies choice to the header repair locates. It
// reports false when the header is already valid, for example because the
// operator bound it to a project secret.
func ApplyNativeHeaderRepair(script *dsl.Script, repair NativeHeaderRepair, choice string) (bool, error) {
	if repair.Omitted {
		return applyOmittedHeaderRepair(script, repair, choice)
	}
	if script == nil || repair.ActionIndex < 0 || repair.ActionIndex >= len(script.Actions) {
		return false, errors.New("header repair no longer matches the inspected script")
	}
	action := &script.Actions[repair.ActionIndex]
	if repair.HeaderIndex < 0 || repair.HeaderIndex >= len(action.HeaderList) {
		return false, errors.New("header repair no longer matches the inspected script")
	}
	header := action.HeaderList[repair.HeaderIndex]
	if repair.HeaderName != "" && header.Name != repair.HeaderName {
		return false, errors.New("header repair no longer matches the inspected script")
	}
	version, err := dsl.EffectiveHTTPVersion(*action, dsl.InheritedHTTPVersion(script))
	if err != nil {
		version = "2"
	}
	code, invalid := headerSyntaxIssue(header.Name, header.Value, version)
	if !invalid {
		return false, nil
	}
	switch choice {
	case HeaderRepairTrim:
		if code != httpprotocol.HeaderListIssueInvalidValue {
			return false, errors.New("an invalid header name can only be removed")
		}
		kept, ok := trimmedHeaderValue(header.Name, header.Value, version)
		if !ok {
			return false, fmt.Errorf("the %s header has no valid leading text to keep; remove it instead", header.Name)
		}
		action.HeaderList[repair.HeaderIndex].Value = kept
	case HeaderRepairRemove:
		action.HeaderList = append(action.HeaderList[:repair.HeaderIndex:repair.HeaderIndex], action.HeaderList[repair.HeaderIndex+1:]...)
	default:
		return false, fmt.Errorf("unknown header repair %q", choice)
	}
	syncHeaderMapEntry(action, header.Name)
	return true, nil
}

// NativeHeaderRepairTargetValue returns the current value of the header repair
// locates, or "" when the location no longer exists.
func NativeHeaderRepairTargetValue(script *dsl.Script, repair NativeHeaderRepair) string {
	if script == nil || repair.ActionIndex < 0 || repair.ActionIndex >= len(script.Actions) {
		return ""
	}
	headers := script.Actions[repair.ActionIndex].HeaderList
	if repair.HeaderIndex < 0 || repair.HeaderIndex >= len(headers) {
		return ""
	}
	return headers[repair.HeaderIndex].Value
}

// markBodyDefaultContentType points a left-out Content-Type's repair at the
// default the request body added in its place, at index. Left-out headers
// that followed it in the source move one place later, so they go back after
// it. diagnostics are in source order.
func markBodyDefaultContentType(diagnostics []NativeDiagnostic, index int) {
	marked := false
	for _, diagnostic := range diagnostics {
		repair := diagnostic.HeaderRepair
		if repair == nil || !repair.Omitted {
			continue
		}
		if marked {
			if repair.HeaderIndex >= index {
				repair.HeaderIndex++
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(repair.HeaderName), "Content-Type") {
			repair.HeaderIndex = index
			repair.ReplacesBodyDefault = true
			marked = true
		}
	}
}

// applyOmittedHeaderRepair puts a left-out header back, trimmed, or confirms
// it stays out. Either choice resolves the finding.
func applyOmittedHeaderRepair(script *dsl.Script, repair NativeHeaderRepair, choice string) (bool, error) {
	if script == nil || repair.ActionIndex < 0 || repair.ActionIndex >= len(script.Actions) {
		return false, errors.New("header repair no longer matches the inspected script")
	}
	switch choice {
	case HeaderRepairRemove:
		return true, nil
	case HeaderRepairTrim:
	default:
		return false, fmt.Errorf("unknown header repair %q", choice)
	}
	if !slices.Contains(repair.Options, HeaderRepairTrim) || repair.TrimmedValue == "" {
		return false, fmt.Errorf("the %s header has no valid leading text to keep; leave it out instead", repair.HeaderName)
	}
	if _, invalid := headerSyntaxIssue(repair.HeaderName, repair.TrimmedValue, ""); invalid {
		return false, errors.New("header repair no longer matches the inspected script")
	}
	action := &script.Actions[repair.ActionIndex]
	if repair.ReplacesBodyDefault {
		if repair.HeaderIndex < 0 || repair.HeaderIndex >= len(action.HeaderList) ||
			!strings.EqualFold(strings.TrimSpace(action.HeaderList[repair.HeaderIndex].Name), "Content-Type") {
			return false, errors.New("header repair no longer matches the inspected script")
		}
		action.HeaderList[repair.HeaderIndex].Value = repair.TrimmedValue
		syncHeaderMapEntry(action, repair.HeaderName)
		return true, nil
	}
	index := min(max(repair.HeaderIndex, 0), len(action.HeaderList))
	headers := make(dsl.HeaderList, 0, len(action.HeaderList)+1)
	headers = append(headers, action.HeaderList[:index]...)
	headers = append(headers, dsl.HeaderEntry{Name: repair.HeaderName, Value: repair.TrimmedValue})
	headers = append(headers, action.HeaderList[index:]...)
	action.HeaderList = headers
	syncHeaderMapEntry(action, repair.HeaderName)
	return true, nil
}

func headerSyntaxIssue(name, value, version string) (httpprotocol.HeaderListIssueCode, bool) {
	issues := httpprotocol.HeaderListIssuesForVersionWithUnresolvedTemplates(
		protocol.HeaderList{{Name: name, Value: value}}, version,
	)
	for _, issue := range issues {
		if issue.Code == httpprotocol.HeaderListIssueInvalidName || issue.Code == httpprotocol.HeaderListIssueInvalidValue {
			return issue.Code, true
		}
	}
	return "", false
}

// trimmedHeaderValue returns a long leading part of value that HTTP accepts,
// without trailing separators. A curl fragment pasted after a cookie
// therefore leaves just the cookie. Cuts are considered only just before a
// separator, quote, control or non-ASCII character, where stray text usually
// starts. Validity is normally lost at one point and not regained, so a
// binary search over those cuts finds the last valid one in a logarithmic
// number of checks; whatever it returns has itself been validated.
func trimmedHeaderValue(name, value, version string) (string, bool) {
	if len(value) > maxTrimmableHeaderValueBytes {
		return "", false
	}
	var cuts []string
	for end := 1; end < len(value); end++ {
		if !headerTrimBoundary(value[end]) {
			continue
		}
		kept := strings.TrimRight(value[:end], " \t;,")
		if kept != "" && (len(cuts) == 0 || cuts[len(cuts)-1] != kept) {
			cuts = append(cuts, kept)
		}
	}
	best := ""
	for low, high := 0, len(cuts)-1; low <= high; {
		middle := low + (high-low)/2
		if _, invalid := headerSyntaxIssue(name, cuts[middle], version); invalid {
			high = middle - 1
			continue
		}
		best = cuts[middle]
		low = middle + 1
	}
	if best != "" {
		return best, true
	}
	// When an early cut is invalid but a later one is not, as in
	// "[::1]:8080/path" for Host, the search above finds nothing. Check the
	// longest cuts directly, a bounded number of them.
	for index, checked := len(cuts)-1, 0; index >= 0 && checked < maxHeaderTrimFallbackChecks; index, checked = index-1, checked+1 {
		if _, invalid := headerSyntaxIssue(name, cuts[index], version); !invalid {
			return cuts[index], true
		}
	}
	return "", false
}

func headerTrimBoundary(b byte) bool {
	if b < 0x20 || b == 0x7f {
		return true
	}
	if b >= 0x80 {
		return utf8.RuneStart(b)
	}
	return strings.IndexByte(" \t;,\"\\'/?#@()<>[]{}", b) >= 0
}

func describeHeaderCharacter(rest string) string {
	rest = strings.TrimLeft(rest, ";,")
	r, _ := utf8.DecodeRuneInString(rest)
	switch {
	case rest == "":
		return "the end of the value"
	case r == '"':
		return `a double quote (")`
	case r == ' ':
		return "a space"
	case r == '\t':
		return "a tab"
	case r == '\r' || r == '\n':
		return "a line break"
	case r < 0x20 || r == 0x7f:
		return "a control character"
	case r == utf8.RuneError:
		return "an invalid byte"
	default:
		return fmt.Sprintf("%q", string(r))
	}
}

// syncHeaderMapEntry keeps the compatibility header map in step with the
// ordered list for one header name. The list is authoritative; the map holds
// its last enabled value for each name.
func syncHeaderMapEntry(action *dsl.Action, name string) {
	if action.Headers == nil {
		return
	}
	identity := strings.TrimSpace(name)
	for key := range action.Headers {
		if strings.EqualFold(strings.TrimSpace(key), identity) {
			delete(action.Headers, key)
		}
	}
	for _, header := range action.HeaderList {
		if header.Enabled != nil && !*header.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(header.Name), identity) {
			for key := range action.Headers {
				if strings.EqualFold(key, strings.TrimSpace(header.Name)) {
					delete(action.Headers, key)
				}
			}
			action.Headers[strings.TrimSpace(header.Name)] = header.Value
		}
	}
}
