// Package jsonextracttest holds the number-extraction fixture that every
// protocol's tests run, so all JSON extraction keeps one contract: a selected
// number comes back as its exact source token.
package jsonextracttest

// NumberCase is one JSON number token that extraction must return verbatim.
type NumberCase struct {
	Name string
	Raw  string
}

// NumberCases covers integers inside and beyond float64's exact range, the
// seven-digit identifiers Go would print in exponent form, and decimal and
// exponent spellings that a float64 round trip would rewrite.
var NumberCases = []NumberCase{
	{Name: "small integer", Raw: "42"},
	{Name: "seven digit identifier", Raw: "1234567"},
	{Name: "date-like integer", Raw: "20261017"},
	{Name: "integer above 2^53", Raw: "9007199254740993"},
	{Name: "64-bit identifier", Raw: "1234567890123456789"},
	{Name: "negative zero", Raw: "-0"},
	{Name: "decimal", Raw: "1.5"},
	{Name: "exponent spelling", Raw: "1e21"},
	{Name: "trailing zeros", Raw: "1.2300"},
}

// Body places raw as the value of "id" and as the only element of "ids".
func Body(raw string) []byte {
	return []byte(`{"id":` + raw + `,"ids":[` + raw + `]}`)
}
