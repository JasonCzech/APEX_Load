package recordingprofile

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestRecordingProfilesDocCoversEveryField keeps docs/RECORDING_PROFILES.md
// honest: every profile field is in its settings table, and every setting the
// VuGen mapping table names exists.
func TestRecordingProfilesDocCoversEveryField(t *testing.T) {
	data, err := os.ReadFile("../../docs/RECORDING_PROFILES.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	fields := map[string]bool{}
	collect := func(typ reflect.Type, prefix string) {
		for i := 0; i < typ.NumField(); i++ {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			switch name {
			case "", "-", "id", "name", "description", "correlation":
				continue
			}
			fields[prefix+name] = true
		}
	}
	collect(reflect.TypeOf(Profile{}), "")
	collect(reflect.TypeOf(CorrelationSettings{}), "correlation.")

	settings, mapping, found := strings.Cut(doc, "## VuGen recording options")
	if !found {
		t.Fatal("the VuGen mapping section is missing")
	}
	for field := range fields {
		if !strings.Contains(settings, "`"+field+"`") {
			t.Errorf("the settings table does not document %s", field)
		}
	}

	code := regexp.MustCompile("`([a-z_.]+)(?::[^`]*)?`")
	rows := 0
	for _, line := range strings.Split(mapping, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 || strings.HasPrefix(strings.TrimSpace(cells[1]), "---") || strings.TrimSpace(cells[1]) == "VuGen option" {
			continue
		}
		rows++
		apex := strings.TrimSpace(cells[2])
		if apex == "" {
			t.Errorf("%q has no APEX setting or reason", strings.TrimSpace(cells[1]))
		}
		for _, match := range code.FindAllStringSubmatch(apex, -1) {
			if token := match[1]; token != "base_url" && !fields[token] {
				t.Errorf("%q names %s, which is not a profile setting", strings.TrimSpace(cells[1]), token)
			}
		}
	}
	if rows < 18 {
		t.Fatalf("the VuGen mapping table has %d rows; the plan's baseline has 18", rows)
	}
}
