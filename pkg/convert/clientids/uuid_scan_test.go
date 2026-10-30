package clientids

import (
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// oracleUUIDPattern and oracleUUIDSpans are the regexp implementation the
// fixed-shape scanner replaced, kept to prove equal output.
var oracleUUIDPattern = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)

func oracleUUIDSpans(text string) [][2]int {
	var spans [][2]int
	for _, match := range oracleUUIDPattern.FindAllStringIndex(text, -1) {
		if match[0] > 0 && isHex(text[match[0]-1]) || match[1] < len(text) && isHex(text[match[1]]) {
			continue
		}
		spans = append(spans, [2]int{match[0], match[1]})
	}
	return spans
}

func oracleUUIDMatches(text string) []int {
	var starts []int
	for _, match := range oracleUUIDPattern.FindAllStringIndex(text, -1) {
		starts = append(starts, match[0])
	}
	return starts
}

func TestUUIDSpansMatchRegexpOracle(t *testing.T) {
	const id = "6f1c2a3b-4d5e-4f60-8172-93a4b5c6d7e8"
	inputs := []string{
		"", "-", "--------", id, id[:35], id[1:], strings.ToUpper(id), "0" + id, id + "0", "g" + id + "g",
		id + id, id + "-" + id, id + "," + id, id[:9] + id, id[:24] + id, "abcdefab-" + id,
		"abcdefab-abcd-abcd-abcd-abcdefabcdefab", "abcdefab-abcd-abcd-abcd-abcdefabcdef-abcd-abcd-abcd-abcdefabcdef",
		"é" + id + "日", "\xff" + id + "\xc3", id[:8] + "\xc3" + id[9:], "６f1c2a3b-4d5e-4f60-8172-93a4b5c6d7e8",
		strings.Repeat("a", 8) + strings.Repeat("-aaaa", 3) + "-" + strings.Repeat("a", 30),
		strings.Repeat("-", 100) + id + strings.Repeat("-", 100),
	}
	rng := rand.New(rand.NewSource(17))
	alphabet := []string{"0", "9", "a", "f", "A", "F", "g", "G", "z", "-", "-", "-", ",", " ", `"`, "é", "日", "\xff", "\xc3", "\x80"}
	for i := 0; i < 50000; i++ {
		var b strings.Builder
		for n := rng.Intn(120); n > 0; n-- {
			switch rng.Intn(10) {
			case 0:
				b.WriteString(id)
			case 1:
				// A UUID with one byte damaged or dropped.
				damaged := []byte(id)
				damaged[rng.Intn(len(damaged))] = alphabet[rng.Intn(len(alphabet))][0]
				b.Write(damaged[:len(damaged)-rng.Intn(2)])
			case 2:
				for k := rng.Intn(14); k > 0; k-- {
					b.WriteByte("0123456789abcdefABCDEF"[rng.Intn(22)])
				}
			default:
				b.WriteString(alphabet[rng.Intn(len(alphabet))])
			}
		}
		inputs = append(inputs, b.String())
	}
	for i := 0; i < 5000; i++ {
		raw := make([]byte, rng.Intn(96))
		for j := range raw {
			raw[j] = byte(rng.Intn(256))
		}
		inputs = append(inputs, string(raw))
	}
	found := 0
	for _, text := range inputs {
		var starts []int
		for match := nextUUID(text, 0); match >= 0; match = nextUUID(text, match+uuidLength) {
			starts = append(starts, match)
		}
		if want := oracleUUIDMatches(text); !reflect.DeepEqual(starts, want) {
			t.Fatalf("UUID matches of %q = %v, want %v", text, starts, want)
		}
		got, want := uuidSpans(text), oracleUUIDSpans(text)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("uuidSpans(%q) = %v, want %v", text, got, want)
		}
		found += len(got)
	}
	if found == 0 {
		t.Fatal("inputs contained no UUIDs")
	}
}
