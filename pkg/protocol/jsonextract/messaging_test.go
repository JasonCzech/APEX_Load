package jsonextract

import "testing"

func TestMessagingJSONPathContainerSemantics(t *testing.T) {
	for _, tc := range []struct{ name, path, body, want string }{
		{"numeric dot key", "$.counts.123", `{"counts":{"123":"ok"}}`, "ok"},
		{"quoted numeric key", `$.counts["123"]`, `{"counts":{"123":"ok"}}`, "ok"},
		{"single quoted key", `$['123']`, `{"123":"ok"}`, "ok"},
		{"array index", "$.items[1]", `{"items":["a","b"]}`, "b"},
		{"numeric dotted array", "$.items.1", `{"items":["a","b"]}`, "b"},
		{"punctuation", `$["a.b"][0]["c/d"]`, `{"a.b":[{"c/d":"ok"}]}`, "ok"},
		{"double quoted closing bracket", `$["a]b"]`, `{"a]b":"ok"}`, "ok"},
		{"single quoted closing bracket", `$['a]b']`, `{"a]b":"ok"}`, "ok"},
		{"escaped double quote", `$["a\"]b"]`, `{"a\"]b":"ok"}`, "ok"},
		{"escaped single quote", `$['a\']b']`, `{"a']b":"ok"}`, "ok"},
		{"escaped backslash before bracket", `$["a\\]b"]`, `{"a\\]b":"ok"}`, "ok"},
		{"exact number", "$.id", `{"id":9007199254740993}`, "9007199254740993"},
		{"root raw", "$", ` { "id": 1 } `, ` { "id": 1 } `},
		{"empty raw", "", `plain text`, `plain text`},
		{"legacy null", "$.id", `{"id":null}`, "<nil>"},
		{"legacy composite", "$.id", `{"id":{"a":1}}`, "map[a:1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MessagingJSONPath(tc.path, []byte(tc.body))
			if err != nil || got != tc.want {
				t.Fatalf("got=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}
func TestMessagingJSONPathErrors(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"$.x", "invalid"}, {"$.x", `{"x":1} {}`},
		{"$.missing", `{"x":1}`}, {"$.x.a", `{"x":1}`},
		{"$.items[1]", `{"items":[]}`}, {"$.items[-1]", `{"items":[1]}`},
		{"$.items[99999999999999999999999999]", `{"items":[1]}`},
		{"$.items[foo]", `{"items":[1]}`}, {"$.items[0", `{"items":[1]}`},
		{`$["unclosed]`, `{}`},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			if _, err := MessagingJSONPath(tc.path, []byte(tc.body)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
