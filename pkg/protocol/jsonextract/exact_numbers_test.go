package jsonextract

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestJSONPathPreservesExactNumbers(t *testing.T) {
	for _, raw := range []string{"1234567", "-1234567", "9007199254740993", "9223372036854775807", "-9223372036854775808", "18446744073709551615", "1.2300", "1e400", "-0", "0.00000000000000000001"} {
		t.Run(raw, func(t *testing.T) {
			for _, tc := range []struct{ body, path, want string }{
				{raw, "$", raw},
				{`{"id":` + raw + `}`, "$.id", raw},
				{`{"ids":[` + raw + `]}`, "$.ids[0]", raw},
				{`{"ids":[` + raw + `]}`, "$.ids", "[" + raw + "]"},
			} {
				got, err := JSONPath(tc.path, []byte(tc.body))
				if err != nil || got != tc.want {
					t.Fatalf("JSONPath(%s,%s)=%q,%v; want %s", tc.path, tc.body, got, err, tc.want)
				}
			}
		})
	}
}

func TestDecodeExactRequiresOneCompleteDocument(t *testing.T) {
	for _, raw := range []string{"", " ", `{"id":1`, `{"id":1}{"id":2}`, `{"id":1} trailing`, `{"id":1} null`} {
		t.Run(raw, func(t *testing.T) {
			var value any
			if err := DecodeExact([]byte(raw), &value); err == nil {
				t.Fatalf("accepted %q", raw)
			}
			if _, err := JSONPath("$", []byte(raw)); err == nil || errors.Is(err, ErrNotFound) {
				t.Fatalf("invalid document error=%v", err)
			}
		})
	}
	var value map[string]any
	if err := DecodeExact([]byte(`{"id":9007199254740993}  `), &value); err != nil {
		t.Fatal(err)
	}
	if value["id"] != json.Number("9007199254740993") {
		t.Fatalf("value=%#v", value)
	}
	if got := Stringify(value["id"]); got != "9007199254740993" {
		t.Fatal(got)
	}
}

func TestJMESPathRetainsNumericOperators(t *testing.T) {
	for _, tc := range []struct{ expression, body, want string }{
		{"sum(items)", `{"items":[1,2.5,3]}`, "6.5"},
		{"sum(items)", `{"items":[1234566,0.5,0.5]}`, "1234567"},
		{"avg(items)", `{"items":[1234566,1234568]}`, "1234567"},
	} {
		t.Run(tc.expression+tc.body, func(t *testing.T) {
			got, err := JMESPath(tc.expression, []byte(tc.body))
			if err != nil || got != tc.want {
				t.Fatalf("got=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}

func TestJMESPathFormatsNumbersAsJSON(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"1234567", "1234567"},
		{"-1234567", "-1234567"},
		{"1234567.25", "1234567.25"},
		{"1.2300", "1.23"},
		{"0", "0"},
		{"-0", "-0"},
		{"0.000001", "0.000001"},
		{"0.0000001", "1e-7"},
		{"100000000000000000000", "100000000000000000000"},
		{"1000000000000000000000", "1e+21"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			for _, expression := range []string{"id", "ids[0]"} {
				got, err := JMESPath(expression, []byte(`{"id":`+tc.raw+`,"ids":[`+tc.raw+`]}`))
				if err != nil || got != tc.want {
					t.Errorf("%s: got=%q err=%v want=%q", expression, got, err, tc.want)
				}
			}
		})
	}
}

func TestStringifyScalarCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, ""},
		{"string", "1234567", "1234567"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"integer", int(1234567), "1234567"},
		{"large integer", int64(9007199254740993), "9007199254740993"},
		{"exact token", json.Number("1.2300"), "1.2300"},
		{"seven digit float", float64(1234567), "1234567"},
		{"negative zero", math.Copysign(0, -1), "-0"},
		{"smallest float", math.SmallestNonzeroFloat64, "5e-324"},
		{"largest float", math.MaxFloat64, "1.7976931348623157e+308"},
		{"not a number", math.NaN(), "NaN"},
		{"positive infinity", math.Inf(1), "+Inf"},
		{"negative infinity", math.Inf(-1), "-Inf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Stringify(tc.value); got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}
