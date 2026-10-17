package main

import (
	"bytes"
	"go/format"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// A variadic method forwards its last argument spread, and a method without
// results returns after calling the fake.
func TestWriteMethodForwardsVariadicAndResultlessCalls(t *testing.T) {
	pkg := types.NewPackage("example.com/store", "store")
	str := types.Typ[types.String]
	variadic := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, pkg, "id", str), types.NewVar(token.NoPos, pkg, "tags", types.NewSlice(str))),
		types.NewTuple(types.NewVar(token.NoPos, pkg, "", types.Universe.Lookup("error").Type())), true)
	resultless := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewVar(token.NoPos, pkg, "id", str)), nil, false)
	im := &imports{byPath: map[string]string{}, names: map[string]string{}}
	var out bytes.Buffer
	o := overlay{Type: "testOverlay"}
	writeMethod(&out, o, types.NewFunc(token.NoPos, pkg, "Tag", variadic), im)
	writeMethod(&out, o, types.NewFunc(token.NoPos, pkg, "Touch", resultless), im)
	source, err := format.Source([]byte("package p\n" + out.String()))
	if err != nil {
		t.Fatalf("generated methods do not parse: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"func (o *testOverlay) Tag(a0 string, a1 ...string) error {",
		"Tag(string, ...string) error",
		"return fake.Tag(a0, a1...)",
		"return o.base().Tag(a0, a1...)",
		"func (o *testOverlay) Touch(a0 string) {",
		"fake.Touch(a0)\n\t\treturn\n",
		"o.base().Touch(a0)\n}",
	} {
		if !strings.Contains(string(source), want) {
			t.Errorf("generated methods lack %q:\n%s", want, source)
		}
	}
}
