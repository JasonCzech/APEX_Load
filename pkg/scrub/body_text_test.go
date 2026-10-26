package scrub

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestBodyTextRedactsInlineCredentialAssignments(t *testing.T) {
	for _, input := range []string{`<script>fetch('/',{body:JSON.stringify({password:'private-value',safe:'visible'})})</script>`, `const access_token = "private-value"; let safe="visible"`, `{"password":"private-value","safe":"visible"}`, `password: 'private\'value', safe:'visible'`} {
		out := BodyText(input)
		if strings.Contains(out, "private") || !strings.Contains(out, "visible") {
			t.Fatalf("%q", out)
		}
	}
	if got := ValueLabel("password:'ordinary-source-label'", Policy{}); got != "password:'ordinary-source-label'" {
		t.Fatal("label behavior changed")
	}
}

func TestHARBodyAssignmentRedactionIncludesBase64Text(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(`const password = 'private-value'; let safe='visible'`))
	raw, err := HAR([]byte(fmt.Sprintf(`{"log":{"entries":[{"request":{"url":"https://example.test/"},"response":{"content":{"mimeType":"application/javascript","encoding":"base64","text":%q}}}]}}`, body)), Policy{})
	if err != nil || strings.Contains(string(raw), body) {
		t.Fatalf("body not scrubbed: %s %v", raw, err)
	}
}
