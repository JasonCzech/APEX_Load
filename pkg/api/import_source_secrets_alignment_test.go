package api

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// A large recorded body carrying many credentials aligns: the work is about
// three passes over the body, not one pass per credential.
func TestLinearAlignmentScalesToLargeBodiesWithManyCredentials(t *testing.T) {
	var raw, projected strings.Builder
	var want []string
	for index := range 200 {
		filler := fmt.Sprintf(`{"ordinary-%d":"%s"},`, index, strings.Repeat("x", 4000))
		credential := fmt.Sprintf("token-%d", index)
		raw.WriteString(filler + `{"token":"` + credential + `"},`)
		projected.WriteString(filler + `{"token":"` + maskedScriptSecret + `"},`)
		want = append(want, credential)
	}
	// 200 credentials across 800 KB: one full scan per credential would be
	// 160M bytes, far past any fixed bound.
	got, ok := extractLinearlyProjectedRedactions(raw.String(), projected.String())
	if !ok || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("large body alignment ok=%t, %d values", ok, len(got))
	}
}

// Input whose literals match everywhere is refused within the work bound
// instead of costing time quadratic in its size.
func TestLinearAlignmentRefusesPathologicalAmbiguityQuickly(t *testing.T) {
	raw := strings.Repeat("a", 1<<20)
	projected := strings.Repeat(maskedScriptSecret+"a", 2000)
	started := time.Now()
	if _, ok := extractLinearlyProjectedRedactions(raw, projected); ok {
		t.Fatal("an ambiguous alignment succeeded")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("ambiguous alignment took %s", elapsed)
	}
}

// Scanning only up to each literal's latest feasible position gives exactly
// the result of enumerating every segmentation.
func TestLinearAlignmentMatchesExhaustiveSegmentation(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	for trial := range 20000 {
		raw := randomAlignmentText(random, random.Intn(14))
		projected := randomProjection(random, raw)
		got, ok := extractLinearlyProjectedRedactions(raw, projected)
		want, wantOK := exhaustiveProjectedRedactions(raw, projected)
		if ok != wantOK || strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("trial %d: raw %q projected %q = %q %t, want %q %t", trial, raw, projected, got, ok, want, wantOK)
		}
	}
}

func randomAlignmentText(random *rand.Rand, length int) string {
	const alphabet = "ab-"
	text := make([]byte, length)
	for index := range text {
		text[index] = alphabet[random.Intn(len(alphabet))]
	}
	return string(text)
}

// randomProjection masks random spans of raw, or sometimes returns unrelated
// text, so both alignable and unalignable pairs are tried.
func randomProjection(random *rand.Rand, raw string) string {
	if random.Intn(5) == 0 {
		return randomAlignmentText(random, random.Intn(6)) + maskedScriptSecret + randomAlignmentText(random, random.Intn(6))
	}
	var out strings.Builder
	masked := false
	for index := 0; index < len(raw); {
		if random.Intn(4) == 0 {
			end := index + random.Intn(len(raw)-index+1)
			out.WriteString(maskedScriptSecret)
			masked = true
			index = end
			if index == len(raw) {
				break
			}
		}
		out.WriteByte(raw[index])
		index++
	}
	if !masked {
		out.WriteString(maskedScriptSecret)
	}
	return out.String()
}

// exhaustiveProjectedRedactions enumerates every way the projection's literal
// text can lie in raw, each sentinel standing for any (possibly empty) run of
// bytes, and returns the values when exactly one way exists. Adjacent
// sentinels are unavailable, as in the linear alignment.
func exhaustiveProjectedRedactions(raw, projected string) ([]string, bool) {
	parts := strings.Split(projected, maskedScriptSecret)
	if len(parts) < 2 || !strings.HasPrefix(raw, parts[0]) {
		return nil, false
	}
	for index := 1; index < len(parts)-1; index++ {
		if parts[index] == "" {
			return nil, false
		}
	}
	var found [][]string
	var walk func(index, cursor int, values []string)
	walk = func(index, cursor int, values []string) {
		if len(found) > 1 {
			return
		}
		if index == len(parts) {
			if cursor == len(raw) {
				found = append(found, append([]string(nil), values...))
			}
			return
		}
		literal := parts[index]
		for position := cursor; position+len(literal) <= len(raw); position++ {
			if raw[position:position+len(literal)] != literal {
				continue
			}
			if index == len(parts)-1 && position+len(literal) != len(raw) {
				continue
			}
			walk(index+1, position+len(literal), append(values, raw[cursor:position]))
			if literal == "" {
				// An empty last literal takes the rest of the scalar once.
				return
			}
		}
	}
	walk(1, len(parts[0]), nil)
	if len(found) != 1 {
		return nil, false
	}
	return found[0], true
}
