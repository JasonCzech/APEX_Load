// Command lrcatalog regenerates the per-family LoadRunner function catalogs in
// pkg/convert/loadrunner/testdata/loadrunner_functions from saved VuGen reference index
// fixtures under tools/lrcatalog/fixtures.
//
// It performs an idempotent upsert: every curated row in an existing catalog CSV is
// preserved verbatim (its support_mode and reason are never clobbered), and only function
// names that appear in a fixture but not yet in the catalog are appended with the family's
// default disposition. This lets a maintainer refresh fixtures from a newer VuGen version
// and have only the genuinely-new functions surface as a reviewable diff.
//
// CI never crawls the live documentation site. See CATALOG_SOURCING.md for the manual
// refresh procedure.
//
// Usage:
//
//	go run ./tools/lrcatalog            # write missing functions into the catalog CSVs
//	go run ./tools/lrcatalog -check     # exit non-zero if any fixture function is missing
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// familyPolicy is the default disposition applied to functions discovered in a fixture but
// not yet present in the catalog. Curated rows always win over these defaults.
type familyPolicy struct {
	family      string
	defaultMode string
	reason      string
}

// policies maps a fixture/catalog stem (fixtures/<stem>.html -> <stem>.csv) to its default
// disposition. Add an entry here when introducing a new protocol family fixture.
var policies = map[string]familyPolicy{
	"web":               {"web", "review_only", "Documented VuGen web function; pending native APEX Load mapping, preserved for review."},
	"socket":            {"socket", "review_only", "Documented VuGen Winsock (lrs_) function; pending native socket mapping, preserved for review."},
	"network_protocols": {"network", "review_only", "Documented VuGen network protocol function; pending native network mapping, preserved for review."},
	"messaging":         {"messaging", "review_only", "Documented VuGen messaging function; pending native messaging mapping, preserved for review."},
	"database_lrd":      {"database", "review_only", "Documented VuGen database (lrd_) function; pending native db_* mapping, preserved for review."},
	"sapgui":            {"sapgui", "review_only", "SAPGUI GUI step has no APEX Load runtime; preserved for manual rework."},
	"citrix":            {"citrix", "review_only", "Citrix ICA step has no APEX Load runtime; preserved for manual rework."},
	"oracle_nca":        {"oracle_nca", "review_only", "Oracle NCA step has no APEX Load runtime; preserved for manual rework."},
	"rte":               {"rte", "review_only", "RTE terminal step has no APEX Load runtime; preserved for manual rework."},
	"flex":              {"flex", "review_only", "Documented VuGen Flex/AMF function; pending native mapping, preserved for review."},
	"vts":               {"vts", "external_required", "Requires an external Virtual Table Server (VTS) integration."},
}

// anchorPattern captures the text of HTML anchors in the reference index fixtures, e.g.
// <a href="...">web_set_user</a> -> web_set_user.
var anchorPattern = regexp.MustCompile(`(?is)<a\b[^>]*>\s*([A-Za-z][A-Za-z0-9_]*)\s*(?:\(\))?\s*</a>`)

// handledExternally lists functions converted by dedicated handlers in the converter/engine
// (not via the catalog dispatch), so they are intentionally absent from the catalog CSVs.
// The generator skips them even when a fixture lists them, avoiding false drift. Keys are
// lowercased to match the case-insensitive catalog.
var handledExternally = map[string]bool{
	"web_url":                   true,
	"web_submit_data":           true,
	"web_submit_form":           true,
	"web_custom_request":        true,
	"web_service_call":          true,
	"web_reg_find":              true,
	"web_add_header":            true,
	"web_add_auto_header":       true,
	"web_cache_cleanup":         true,
	"web_cleanup_cookies":       true,
	"web_set_sockets_option":    true,
	"web_set_timeout":           true,
	"web_reg_save_param":        true,
	"web_reg_save_param_ex":     true,
	"web_reg_save_param_regexp": true,
	"web_reg_save_param_json":   true,
	"web_reg_save_param_xpath":  true,
}

func main() {
	fixturesDir := flag.String("fixtures", "tools/lrcatalog/fixtures", "directory of saved VuGen reference index HTML fixtures")
	catalogDir := flag.String("catalog", "pkg/convert/loadrunner/testdata/loadrunner_functions", "directory of per-family catalog CSV files")
	check := flag.Bool("check", false, "report (and exit non-zero) when fixtures contain functions missing from the catalog, without writing")
	flag.Parse()

	changed, err := Generate(*fixturesDir, *catalogDir, *check)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lrcatalog:", err)
		os.Exit(1)
	}
	if *check && len(changed) > 0 {
		fmt.Fprintln(os.Stderr, "lrcatalog: catalog is out of date with fixtures; run `go run ./tools/lrcatalog`:")
		for _, line := range changed {
			fmt.Fprintln(os.Stderr, "  "+line)
		}
		os.Exit(1)
	}
	for _, line := range changed {
		fmt.Println(line)
	}
}

// Generate reconciles each fixture against its catalog CSV. In check mode it reports the
// functions that would be added without modifying any files; otherwise it appends them.
// It returns one "family: fn1, fn2" description per catalog that has (or would have) changes.
func Generate(fixturesDir, catalogDir string, check bool) ([]string, error) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		return nil, fmt.Errorf("read fixtures dir: %w", err)
	}

	var summary []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".html")
		policy, ok := policies[stem]
		if !ok {
			return nil, fmt.Errorf("no family policy for fixture %q (add it to policies in tools/lrcatalog/main.go)", entry.Name())
		}

		fixturePath := filepath.Join(fixturesDir, entry.Name())
		names, err := extractFunctionNames(fixturePath)
		if err != nil {
			return nil, err
		}

		catalogPath := filepath.Join(catalogDir, stem+".csv")
		existing, content, err := readCatalogNames(catalogPath)
		if err != nil {
			return nil, err
		}

		var missing []string
		for _, name := range names {
			key := strings.ToLower(name)
			if handledExternally[key] {
				continue
			}
			if _, present := existing[key]; !present {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			continue
		}
		sort.Strings(missing)
		summary = append(summary, fmt.Sprintf("%s: %s", stem, strings.Join(missing, ", ")))

		if check {
			continue
		}
		if err := appendCatalogRows(catalogPath, content, missing, policy); err != nil {
			return nil, err
		}
	}
	sort.Strings(summary)
	return summary, nil
}

// extractFunctionNames returns the function-like anchor texts from a fixture, de-duplicated
// in first-seen order. Function-like means it contains an underscore (e.g. web_set_user).
func extractFunctionNames(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture %s: %w", path, err)
	}
	seen := make(map[string]bool)
	var names []string
	for _, match := range anchorPattern.FindAllStringSubmatch(string(data), -1) {
		token := strings.TrimSpace(match[1])
		if !strings.Contains(token, "_") {
			continue
		}
		key := strings.ToLower(token)
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, token)
	}
	return names, nil
}

// readCatalogNames returns the set of function names already present in a catalog CSV
// (lowercased) plus the raw file content. A missing file is treated as empty.
func readCatalogNames(path string) (map[string]struct{}, string, error) {
	names := make(map[string]struct{})
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return names, "", nil
		}
		return nil, "", fmt.Errorf("read catalog %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		names[strings.ToLower(strings.TrimSpace(parts[0]))] = struct{}{}
	}
	return names, string(data), nil
}

// appendCatalogRows writes the existing content back followed by one default-disposition row
// per missing function, preserving all curated rows and their order.
func appendCatalogRows(path, content string, missing []string, policy familyPolicy) error {
	var b strings.Builder
	b.WriteString(content)
	if content != "" && !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	if content == "" {
		fmt.Fprintf(&b, "# OpenText VuGen %s functions (generated by tools/lrcatalog).\n", policy.family)
		b.WriteString("# Format: name,family,support_mode[,reason]\n")
	}
	for _, name := range missing {
		fmt.Fprintf(&b, "%s,%s,%s,%s\n", name, policy.family, policy.defaultMode, policy.reason)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write catalog %s: %w", path, err)
	}
	return nil
}
