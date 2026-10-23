package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func loadTestSchema(t *testing.T) *schema {
	t.Helper()
	s, err := loadSchema()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestGroupingCoversTheSchema(t *testing.T) {
	if err := validate(loadTestSchema(t)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsUnclassifiedIDColumns(t *testing.T) {
	s := loadTestSchema(t)
	s.tables["reusable_blocks"].columns = append(s.tables["reusable_blocks"].columns, column{name: "widget_id", typ: "TEXT"})
	err := validate(s)
	if err == nil || !strings.Contains(err.Error(), "reusable_blocks.widget_id is not classified") {
		t.Fatalf("validate() = %v, want an unclassified widget_id", err)
	}
}

func TestReferencesWithoutAConstraintAreDashedAndOptional(t *testing.T) {
	doc := render(loadTestSchema(t))
	for _, line := range []string{
		// Overview edges take their style from the schema, not from the spine list.
		`capacity_reservations |o..o{ tests : "timeslot_id"`,
		`execution_targets |o..o{ fleet_pools : "execution_target_id"`,
		// NOT NULL DEFAULT '' still means "none" for a column with no constraint.
		`cluster_connections |o..o{ fleet_pools : "cluster_id"`,
		// project_id without a constraint is still listed as a reference.
		"`project_id` → `projects` (no constraint)",
	} {
		if !strings.Contains(doc, line) {
			t.Errorf("doc is missing %q", line)
		}
	}
}

func TestRenderIsDeterministicWithOneHeadingPerDomain(t *testing.T) {
	s := loadTestSchema(t)
	doc := render(s)
	if doc != render(s) {
		t.Fatal("render is not deterministic")
	}
	if !strings.HasPrefix(doc, "# Data Model\n") || strings.Count(doc, "\n# ") != 0 {
		t.Fatal("expected exactly one H1 at the top")
	}
	last := -1
	for _, d := range domains {
		i := strings.Index(doc, "\n## "+d.title+"\n")
		if i <= last {
			t.Fatalf("section %q missing or out of order", d.title)
		}
		last = i
	}
	for name := range s.tables {
		if !strings.Contains(doc, "| `"+name+"` |") {
			t.Fatalf("table %s has no reference row", name)
		}
	}
}

// These mirror ENTITY_OPEN, ATTRIBUTE and RELATIONSHIP in
// dashboard/src/components/entityDiagramModel.ts. A line they reject makes the
// Help center show the whole diagram as source instead of drawing it.
var (
	helpEntityOpen   = regexp.MustCompile(`^([A-Za-z_][\w-]*)\s*\{$`)
	helpAttribute    = regexp.MustCompile(`^(\S+)\s+([A-Za-z_][\w-]*)(?:\s+((?:PK|FK|UK)(?:\s*,\s*(?:PK|FK|UK))*))?(?:\s+"([^"]*)")?$`)
	helpRelationship = regexp.MustCompile(`^([A-Za-z_][\w-]*)\s+(\|[o|])(--|\.\.)([o|][|{])\s+([A-Za-z_][\w-]*)\s*:\s*(?:"([^"]*)"|(\S+))$`)
)

func TestDiagramsOnlyUseTheMermaidSubsetHelpRenders(t *testing.T) {
	s := loadTestSchema(t)
	doc := render(s)
	blocks := strings.Split(doc, "```mermaid\n")[1:]
	want := 1 // the overview
	for _, d := range domains {
		if diagram, _, _ := renderDomain(s, d); diagram != "" {
			want++
		}
	}
	if len(blocks) != want {
		t.Fatalf("got %d diagrams, want the overview plus one per drawn domain (%d)", len(blocks), want)
	}
	for _, block := range blocks {
		body, _, ok := strings.Cut(block, "```\n")
		if !ok || !strings.HasPrefix(body, "erDiagram\n") {
			t.Fatalf("diagram is not a closed erDiagram block:\n%s", body)
		}
		inEntity := false
		for _, line := range strings.Split(strings.TrimSpace(body), "\n")[1:] {
			line = strings.TrimSpace(line)
			switch {
			case inEntity && line == "}":
				inEntity = false
			case inEntity && helpAttribute.MatchString(line):
			case !inEntity && helpEntityOpen.MatchString(line):
				inEntity = true
			case !inEntity && helpRelationship.MatchString(line):
			default:
				t.Fatalf("Help cannot draw diagram line %q", line)
			}
		}
		if inEntity {
			t.Fatal("diagram leaves an entity block open")
		}
	}
}

func TestHelpSubsetRejectsWhatHelpCannotDraw(t *testing.T) {
	for _, line := range []string{`TEXT a b PK`, `TEXT id XK`} {
		if helpAttribute.MatchString(line) {
			t.Errorf("attribute %q should be rejected", line)
		}
	}
	for _, line := range []string{`a }o--|| b : "x"`, `a ||--o{ b : two words`, `a ||-o{ b : "x"`} {
		if helpRelationship.MatchString(line) {
			t.Errorf("relationship %q should be rejected", line)
		}
	}
}

func TestCommittedDocIsFresh(t *testing.T) {
	got, err := os.ReadFile("../../" + defaultOut)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != render(loadTestSchema(t)) {
		t.Fatalf("%s is stale; run `go generate ./pkg/storage`", defaultOut)
	}
}
