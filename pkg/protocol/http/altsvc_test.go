package http

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseAltSvc(t *testing.T) {
	day := DefaultAltSvcMaxAge
	cases := []struct {
		name   string
		values []string
		want   AltSvc
	}{
		{name: "empty", values: nil, want: AltSvc{}},
		{name: "same host h3", values: []string{`h3=":443"`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h3", Port: 443, MaxAge: day}}}},
		{name: "max age and persist", values: []string{`h3=":8443"; ma=60; persist=1`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h3", Port: 8443, MaxAge: time.Minute, Persist: true}}}},
		{name: "google style list", values: []string{`h3=":443"; ma=2592000,h3-29=":443"; ma=2592000`},
			want: AltSvc{Entries: []AltSvcEntry{
				{ProtocolID: "h3", Port: 443, MaxAge: 30 * 24 * time.Hour},
				{ProtocolID: "h3-29", Port: 443, MaxAge: 30 * 24 * time.Hour},
			}}},
		{name: "multiple field lines", values: []string{`h2=":443"`, `h3=":443"`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h2", Port: 443, MaxAge: day}, {ProtocolID: "h3", Port: 443, MaxAge: day}}}},
		{name: "other host lowercased", values: []string{`h3="ALT.Example.TEST:443"`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h3", Host: "alt.example.test", Port: 443, MaxAge: day}}}},
		{name: "ipv6 authority", values: []string{`h3="[2001:db8::1]:443"`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h3", Host: "2001:db8::1", Port: 443, MaxAge: day}}}},
		{name: "percent encoded protocol id", values: []string{`w%3Dx%3Ay=":443"`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "w=x:y", Port: 443, MaxAge: day}}}},
		{name: "huge age clamped", values: []string{`h3=":443"; ma=99999999999`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h3", Port: 443, MaxAge: maxAltSvcMaxAge}}}},
		{name: "duplicate param first wins", values: []string{`h3=":443"; ma=10; ma=20`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h3", Port: 443, MaxAge: 10 * time.Second}}}},
		{name: "quoted param value", values: []string{`h3=":443"; ma="30"`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h3", Port: 443, MaxAge: 30 * time.Second}}}},
		{name: "unknown param ignored", values: []string{`h3=":443"; v="2,3"`},
			want: AltSvc{Entries: []AltSvcEntry{{ProtocolID: "h3", Port: 443, MaxAge: day}}}},
		{name: "clear", values: []string{"clear"}, want: AltSvc{Clear: true}},
		{name: "clear mixed with alternatives is malformed", values: []string{`clear, h3=":443"`}, want: AltSvc{Invalid: 2}},
		{name: "port zero", values: []string{`h3=":0"`}, want: AltSvc{Invalid: 1}},
		{name: "port too large", values: []string{`h3=":65536"`}, want: AltSvc{Invalid: 1}},
		{name: "signed port", values: []string{`h3=":+443"`}, want: AltSvc{Invalid: 1}},
		{name: "unquoted authority", values: []string{`h3=:443`}, want: AltSvc{Invalid: 1}},
		{name: "missing port", values: []string{`h3="example.test"`}, want: AltSvc{Invalid: 1}},
		{name: "bad ma skips alternative", values: []string{`h3=":443"; ma=-1, h2=":443"`},
			want: AltSvc{Invalid: 1, Entries: []AltSvcEntry{{ProtocolID: "h2", Port: 443, MaxAge: day}}}},
		{name: "unbracketed ipv6", values: []string{`h3="::1:443"`}, want: AltSvc{Invalid: 1}},
		{name: "path in host", values: []string{`h3="evil/x:443"`}, want: AltSvc{Invalid: 1}},
		{name: "unterminated quote", values: []string{`h3=":443`}, want: AltSvc{Invalid: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAltSvc(tc.values)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseAltSvc(%q)\n got  %+v\n want %+v", tc.values, got, tc.want)
			}
		})
	}
}

func TestParseAltSvcBounds(t *testing.T) {
	many := strings.TrimSuffix(strings.Repeat(`h3=":443",`, 40), ",")
	got := ParseAltSvc([]string{many})
	if len(got.Entries) != maxAltSvcEntries || !got.Truncated {
		t.Fatalf("entries=%d truncated=%v", len(got.Entries), got.Truncated)
	}
	huge := `h3=":443"; x="` + strings.Repeat("a", maxAltSvcFieldBytes) + `"`
	if got := ParseAltSvc([]string{huge}); !got.Truncated || len(got.Entries) != 0 {
		t.Fatalf("oversized field = %+v", got)
	}
}

func TestAltSvcAdvertisesH3(t *testing.T) {
	cases := map[string]bool{
		`h3=":443"`:                            true,
		`h2=":443", h3="alt.example.test:443"`: true,
		`h3-29=":443"`:                         false,
		`h2=":443"`:                            false,
		`clear`:                                false,
		``:                                     false,
	}
	for value, want := range cases {
		if got := AltSvcAdvertisesH3([]string{value}); got != want {
			t.Errorf("AltSvcAdvertisesH3(%q) = %v, want %v", value, got, want)
		}
	}
}

func FuzzParseAltSvc(f *testing.F) {
	for _, seed := range []string{`h3=":443"; ma=60`, `clear`, `h3="[::1]:8443"; persist=1, h2=":443"`, `a="\"b\":1"`, `%zz=":1"`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		got := ParseAltSvc([]string{value})
		if len(got.Entries) > maxAltSvcEntries {
			t.Fatalf("entries = %d", len(got.Entries))
		}
		for _, entry := range got.Entries {
			if entry.Port < 1 || entry.Port > 65535 || entry.ProtocolID == "" || entry.MaxAge < 0 || entry.MaxAge > maxAltSvcMaxAge {
				t.Fatalf("invalid entry %+v from %q", entry, value)
			}
		}
		if got.Clear && len(got.Entries) > 0 {
			t.Fatalf("clear with entries from %q", value)
		}
	})
}
