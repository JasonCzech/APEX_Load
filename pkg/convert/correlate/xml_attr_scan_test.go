package correlate

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The windowed scan must return exactly what the unanchored pattern returns:
// the same matches, order, submatch offsets and count limit.
func TestXMLAttrTokenMatchesMatchesRegexpOracle(t *testing.T) {
	cases := []string{
		``,
		`token="abcdef"`,
		`token="abcde"`,
		`<a ns:sessionId="abcdefgh" other:requestId = "ghijklmn">`,
		`x token =  "abcdefgh"y id="zzzzzzzz"`,
		`a="xx id="yyyyyyyy"`,
		`a="xxxxxx id=" token="abcdefgh"`,
		`id="abcdefgh`,
		`=""=""==" "id="abcdefgh"`,
		"ſeſſion=\"abcdefgh\" toKen=\"abcdefgh\"",
		"\xc5token=\"abcdefgh\" \xf0\x9f\x98ſtate=\"abcdefgh\"",
		`é-state="abcdefgh" état="abcdefgh" .id="abcdefgh" :id="abcdefgh" a:b:id="abcdefgh"`,
		"var i=a<width?\"left-aligned\":\"right\";n.sid=\"0123456789\";",
		"id\t\n\f\r=\r\n\"abcdefgh\"",
		"id\v=\"abcdefgh\"",
		strings.Repeat(`id="abcdefgh" `, 300),
	}
	alphabet := []string{
		"=", "=", `"`, `"`, " ", "\t", "\n", ":", "-", ".", "_", "<", ">", "/", "a", "b", "x", "0", "9",
		"id", "Id", "token", "toKen", "ſtate", "session", "csrf", "nonce", "requestid", "é", "\xc5", "\xe2\x84",
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for j := rng.Intn(60); j >= 0; j-- {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		cases = append(cases, b.String())
	}
	for _, body := range cases {
		for _, n := range []int{-1, 0, 1, 2, 3} {
			want := xmlAttrTokenPattern.FindAllStringSubmatchIndex(body, n)
			got := xmlAttrTokenMatches(body, n)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("n=%d body=%q\n got %v\nwant %v", n, body, got, want)
			}
		}
	}
}

func TestXMLAttrTokenMatchesScalesOnMinifiedJavaScript(t *testing.T) {
	var b strings.Builder
	chunk := `function r(e,t){var n=e.length,o=t<n?t:n;return{state:o,id:e[0],w:width=n}}a.sid="0123456789";if(i<width&&x.state>0)k.call(this,"x-y");`
	for b.Len() < 8<<20 {
		b.WriteString(chunk)
	}
	body := b.String()
	start := time.Now()
	got := xmlAttrTokenMatches(body, maxCorrelationCandidatesPerObservation+1)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("windowed attribute scan over %d bytes took %s", len(body), elapsed)
	}
	if len(got) == 0 {
		t.Fatal("expected the sid attribute-like assignments to match")
	}
	if want := xmlAttrTokenPattern.FindAllStringSubmatchIndex(body, maxCorrelationCandidatesPerObservation+1); !reflect.DeepEqual(got, want) {
		t.Fatal("windowed scan diverged from the pattern on minified JavaScript")
	}
}
