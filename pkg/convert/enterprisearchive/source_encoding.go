package enterprisearchive

import (
	"encoding/base64"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"golang.org/x/text/encoding/charmap"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// Decode only declared text, never assets or the original uploaded bundle.
// Missing declarations accept UTF-8/ASCII only; undefined CP1252 bytes fail
// instead of silently replacing source characters.
func decodeArchiveText(files map[string]string, cfgName string) (string, error) {
	sections := ParseConfigSections(files[cfgName])
	encoding := runtimeSectionValue(findSection(sections, "General"), "Encoding")
	codepage := runtimeSectionValue(findMergedSection(sections, "WEB", "Web"), "RecorderWinCodePage")
	cp1252 := strings.EqualFold(encoding, "ANSI") && codepage == "1252"
	if encoding != "" && !cp1252 && !strings.EqualFold(encoding, "UTF-8") && !strings.EqualFold(encoding, "UTF8") {
		return "", fmt.Errorf("unsupported or ambiguous source encoding; ANSI requires an explicit supported RecorderWinCodePage (1252)")
	}
	for _, conflict := range runtimeConfigCaseFoldConflicts(files[cfgName]) {
		if strings.EqualFold(conflict.key, "Encoding") || strings.EqualFold(conflict.key, "RecorderWinCodePage") {
			return "", fmt.Errorf("conflicting source encoding declarations")
		}
	}
	// Decode metadata first: parameter filenames themselves may contain ANSI text.
	decoded := make(map[string]bool)
	decode := func(name string) error {
		if decoded[name] {
			return nil
		}
		raw := files[name]
		if strings.HasPrefix(raw, "\xef\xbb\xbf") {
			if cp1252 {
				return fmt.Errorf("source text BOM conflicts with declared ANSI encoding in %s", name)
			}
			raw = strings.TrimPrefix(raw, "\xef\xbb\xbf")
		}
		if cp1252 {
			for i := 0; i < len(raw); i++ {
				switch raw[i] {
				case 0x81, 0x8d, 0x8f, 0x90, 0x9d:
					return fmt.Errorf("undefined Windows-1252 byte in source text %s", name)
				}
			}
			decoded, e := charmap.Windows1252.NewDecoder().String(raw)
			if e != nil {
				return e
			}
			files[name] = decoded
		} else {
			if !utf8.ValidString(raw) {
				return fmt.Errorf("non-UTF-8 text in %s requires an unambiguous supported code page", name)
			}
			files[name] = raw
		}
		decoded[name] = true
		return nil
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch strings.ToLower(path.Ext(name)) {
		case ".c", ".h", ".cfg", ".usp", ".usr", ".prm":
			if err := decode(name); err != nil {
				return "", err
			}
		}
	}
	for _, name := range names {
		if !strings.EqualFold(path.Ext(name), ".prm") {
			continue
		}
		for _, section := range ParseConfigSections(files[name]) {
			for key, value := range section {
				switch strings.ToLower(key) {
				case "table", "filename", "file", "datafile", "sourcefile":
					if resolved, _, ok := findFileEntry(files, value); ok {
						if err := decode(resolved); err != nil {
							return "", err
						}
					}
				}
			}
		}
	}
	if cp1252 {
		return "windows-1252", nil
	}
	return "", nil
}
func encodeImportedBody(action *dsl.Action, fn ParsedFunction, encoding string) error {
	if encoding != "windows-1252" || fn.JavaOrigin || fn.Body == "" {
		return nil
	}
	if fn.itemDataURLForm {
		// Parser-created form literals already use percent encoding. Re-encode each
		// component without touching separators or deferred parameter templates.
		pairs := strings.Split(action.Body, "&")
		for i, pair := range pairs {
			parts := strings.SplitN(pair, "=", 2)
			for j, part := range parts {
				text, e := url.QueryUnescape(part)
				if e != nil {
					return e
				}
				raw, e := charmap.Windows1252.NewEncoder().String(text)
				if e != nil {
					return e
				}
				parts[j] = encodeSourceFormComponent(raw)
			}
			pairs[i] = strings.Join(parts, "=")
		}
		action.Body = strings.Join(pairs, "&")
		action.BodyEncoding = encoding
		return nil
	}
	// A source escape such as \x81 names a code-page byte, so the source
	// spelling is encoded first and its escapes are decoded in byte space.
	source := fn.Body
	if fn.RawBody != "" {
		source = fn.RawBody
	}
	raw, e := charmap.Windows1252.NewEncoder().String(source)
	if e != nil {
		return e
	}
	if fn.RawBody != "" {
		// A spelling the decoder rejects stays verbatim, as the parser keeps it.
		if decoded, ok := decodeCStringContent(raw); ok {
			raw = decoded
		}
	}
	// Undefined code-page bytes are valid binary payload bytes, but not text.
	// Fixed payloads can be represented losslessly as base64; mixed binary and
	// deferred parameters require explicit repair instead of replacement.
	text, e := charmap.Windows1252.NewDecoder().String(raw)
	if e != nil {
		return e
	}
	encoded, e := charmap.Windows1252.NewEncoder().String(text)
	if e != nil || encoded != raw {
		if strings.Contains(convertParamReferences(raw), "{{") {
			return fmt.Errorf("binary body with deferred parameters requires explicit byte encoding")
		}
		action.Body = base64.StdEncoding.EncodeToString([]byte(raw))
		action.BodyEncoding = "base64"
		return nil
	}
	action.Body = convertParamReferences(text)
	action.BodyEncoding = encoding
	return nil
}
