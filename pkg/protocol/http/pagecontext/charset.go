package pagecontext

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// Decode returns an HTML document as UTF-8 with the name of the encoding it
// was declared in: a byte-order mark, the Content-Type charset, or a <meta>
// charset in the first 1024 bytes, as a browser determines it. An undeclared
// document is taken as UTF-8.
func Decode(body []byte, contentType string) ([]byte, string) {
	enc, name, certain := charset.DetermineEncoding(body, contentType)
	if !certain {
		label := metaCharset(body)
		if label == "" {
			return body, "utf-8"
		}
		if enc, name = charset.Lookup(label); enc == nil {
			return body, "utf-8"
		}
	}
	if name == "utf-8" || enc == nil {
		return body, "utf-8"
	}
	decoded, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		return body, "utf-8"
	}
	return decoded, name
}

// metaCharset returns the charset a <meta> tag in the first 1024 bytes
// declares, or "".
func metaCharset(body []byte) string {
	if len(body) > 1024 {
		body = body[:1024]
	}
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokenizer.TagName()
			if string(name) != "meta" {
				continue
			}
			attrs := map[string]string{}
			for hasAttr {
				var key, value []byte
				key, value, hasAttr = tokenizer.TagAttr()
				attrs[strings.ToLower(string(key))] = string(value)
			}
			if label := strings.TrimSpace(attrs["charset"]); label != "" {
				return label
			}
			if strings.EqualFold(strings.TrimSpace(attrs["http-equiv"]), "content-type") {
				if _, after, found := strings.Cut(strings.ToLower(attrs["content"]), "charset="); found {
					return strings.Trim(strings.TrimSpace(strings.SplitN(after, ";", 2)[0]), `"'`)
				}
			}
		}
	}
}

// EncodeFields encodes form field names and values in the page's character
// encoding, as a browser submits a form; a character the encoding lacks is
// sent as a decimal character reference (&#NNNN;).
func EncodeFields(fields []Field, charsetName string) []Field {
	enc, name := charset.Lookup(charsetName)
	if enc == nil || name == "utf-8" {
		return fields
	}
	encoder := enc.NewEncoder()
	encode := func(text string) string {
		var out strings.Builder
		for _, r := range text {
			encoded, err := encoder.String(string(r))
			if err != nil || r == utf8.RuneError {
				fmt.Fprintf(&out, "&#%d;", r)
				continue
			}
			out.WriteString(encoded)
		}
		return out.String()
	}
	out := make([]Field, len(fields))
	for index, field := range fields {
		out[index] = Field{Name: encode(field.Name), Value: encode(field.Value)}
	}
	return out
}
