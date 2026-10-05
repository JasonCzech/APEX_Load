package har

import (
	"encoding/json"
	"strings"
	"testing"
)

const salvageEntry = `{"request":{"method":"GET","url":"https://example.test/%s","headers":[]},"response":{"status":200,"headers":[],"content":{"text":"ok"}}}`

func salvageHAR(entries ...string) string {
	items := make([]string, len(entries))
	for index, name := range entries {
		items[index] = strings.Replace(salvageEntry, "%s", name, 1)
	}
	return `{"log":{"version":"1.2","creator":{"name":"synthetic","version":"1"},"entries":[` + strings.Join(items, ",") + `],"comment":"after"}}`
}

func TestSalvageTruncatedKeepsCompleteEntries(t *testing.T) {
	full := salvageHAR("a", "b", "c")
	cutInThird := full[:strings.Index(full, "/c")]
	cutAfterSecond := full[:strings.Index(full, `,{"request":{"method":"GET","url":"https://example.test/c"`)]
	for name, tc := range map[string]struct {
		data    string
		entries int
	}{
		"inside an entry":          {cutInThird, 2},
		"right after an entry":     {cutAfterSecond, 2},
		"after an entry and comma": {cutAfterSecond + ",", 2},
		"after the entries":        {full[:strings.Index(full, `,"comment"`)+3], 3},
		"inside a later field":     {full[:strings.Index(full, `"after"`)+3], 3},
		"with a byte order mark":   {"\uFEFF" + cutInThird, 2},
	} {
		salvaged, report, ok := SalvageTruncated([]byte(tc.data))
		if !ok || report.Entries != tc.entries {
			t.Fatalf("%s: ok=%t report=%+v", name, ok, report)
		}
		var decoded struct {
			Log struct {
				Version string            `json:"version"`
				Entries []json.RawMessage `json:"entries"`
			} `json:"log"`
		}
		if err := json.Unmarshal(salvaged, &decoded); err != nil || decoded.Log.Version != "1.2" || len(decoded.Log.Entries) != tc.entries {
			t.Fatalf("%s: salvaged HAR = %s (%v)", name, salvaged, err)
		}
		if report.DroppedBytes < 0 || report.DroppedBytes >= len(tc.data) {
			t.Fatalf("%s: dropped bytes = %d", name, report.DroppedBytes)
		}
		if !strings.Contains(report.Warning(), "truncated") || !strings.Contains(report.Hint(), "salvage_truncated=true") {
			t.Fatalf("%s: messages = %q / %q", name, report.Warning(), report.Hint())
		}
	}
}

func TestSalvageTruncatedRefusesOtherFiles(t *testing.T) {
	full := salvageHAR("a", "b")
	for name, data := range map[string]string{
		"valid":                 full,
		"cut before entries":    full[:strings.Index(full, `"creator"`)+15],
		"cut in the first":      full[:strings.Index(full, "/a")],
		"malformed in middle":   strings.Replace(full, `"status":200`, `"status":}`, 1),
		"trailing garbage":      full + "xyz",
		"malformed after":       strings.Replace(full, `"comment":"after"`, `"comment":}`, 1),
		"not a HAR object":      `[1,2`,
		"entries is not a list": `{"log":{"entries":{"a":1`,
	} {
		if _, report, ok := SalvageTruncated([]byte(data)); ok {
			t.Fatalf("%s salvaged: %+v", name, report)
		}
	}
}
