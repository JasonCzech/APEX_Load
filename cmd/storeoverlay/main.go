// Command storeoverlay generates pkg/api's test store overlays. Run via
// `go generate ./pkg/api`.
//
// A test fake that embeds *storage.SQLiteStore or storage.Store makes the
// compiler build a wrapper for every promoted method, twice (value and pointer
// receivers), per fake: about 200,000 functions for pkg/api's fakes. With them
// the package's test compile peaked at 8-9.5 GB and took three times as long.
// Fakes instead hold the store in a field of the same name and are wrapped in
// an overlay, generated here once per base. An overlay has exactly its base's method set, so storage's
// optional-interface assertions see what they saw when the fake embedded the
// store, and each method calls the fake's own method of that name and
// signature when it has one, else the store in the fake's field.
//
//	-out PATH   output path (default store_overlay_test.go)
//	-check      do not write; exit 1 if the file at -out differs from the output
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const storagePath = "github.com/jasonczech/apexload/pkg/storage"

// overlay is one generated type: Base is the storage type a fake used to embed,
// Field the name of the field that now holds it.
type overlay struct {
	Type, Constructor, Base, Field string
	Pointer                        bool
}

var overlays = []overlay{
	{Type: "sqliteStoreOverlay", Constructor: "sqliteOverlay", Base: "SQLiteStore", Field: "SQLiteStore", Pointer: true},
	{Type: "storeInterfaceOverlay", Constructor: "storeOverlay", Base: "Store", Field: "Store"},
}

func main() {
	out := flag.String("out", "store_overlay_test.go", "output path")
	check := flag.Bool("check", false, "exit 1 if -out is stale instead of writing it")
	flag.Parse()
	source, err := render()
	if err != nil {
		fmt.Fprintln(os.Stderr, "storeoverlay:", err)
		os.Exit(1)
	}
	if *check {
		current, err := os.ReadFile(*out)
		if err != nil || !bytes.Equal(current, source) {
			fmt.Fprintf(os.Stderr, "storeoverlay: %s is stale; run go generate ./pkg/api\n", *out)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*out, source, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "storeoverlay:", err)
		os.Exit(1)
	}
}

// loadStorage type-checks pkg/storage from the export data `go list` builds.
func loadStorage() (*types.Package, error) {
	cmd := exec.Command("go", "list", "-export", "-deps", "-json=ImportPath,Export", storagePath)
	cmd.Stderr = os.Stderr
	listing, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	exports := map[string]string{}
	decoder := json.NewDecoder(bytes.NewReader(listing))
	for {
		var p struct{ ImportPath, Export string }
		if err := decoder.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		exports[p.ImportPath] = p.Export
	}
	lookup := func(path string) (io.ReadCloser, error) {
		file := exports[path]
		if file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	}
	return importer.ForCompiler(token.NewFileSet(), "gc", lookup).Import(storagePath)
}

type imports struct {
	byPath map[string]string
	names  map[string]string
}

// qualify names a package for the generated file, aliasing a second package
// that shares a name.
func (im *imports) qualify(p *types.Package) string {
	if name, ok := im.byPath[p.Path()]; ok {
		return name
	}
	name := p.Name()
	for i := 2; im.names[name] != ""; i++ {
		name = fmt.Sprintf("%s%d", p.Name(), i)
	}
	im.byPath[p.Path()], im.names[name] = name, p.Path()
	return name
}

func render() ([]byte, error) {
	storage, err := loadStorage()
	if err != nil {
		return nil, err
	}
	im := &imports{byPath: map[string]string{}, names: map[string]string{}}
	im.qualify(types.NewPackage("reflect", "reflect"))
	im.qualify(types.NewPackage("sync", "sync"))
	var body bytes.Buffer
	for _, o := range overlays {
		object := storage.Scope().Lookup(o.Base)
		if object == nil {
			return nil, fmt.Errorf("storage.%s not found", o.Base)
		}
		base := object.Type()
		if o.Pointer {
			base = types.NewPointer(base)
		}
		baseName := types.TypeString(base, im.qualify)
		fmt.Fprintf(&body, "\n// %s presents a fake that holds a %s in its %s field\n// as that store's whole method set. Build it with %s.\n", o.Type, baseName, o.Field, o.Constructor)
		fmt.Fprintf(&body, "type %s struct {\n\tfake  any\n\tfield reflect.Value\n}\n\n", o.Type)
		fmt.Fprintf(&body, "// %ss keeps one overlay per comparable fake, so the server's store\n// identity checks see one store for one fake, as they did when it embedded\n// the store.\nvar %ss sync.Map\n\n", o.Type, o.Type)
		fmt.Fprintf(&body, "// %s wraps fake, a struct (or a pointer to one, so later assignments\n// show) whose %s field, its own or an embedded struct's, holds the\n// %s its methods do not override.\n", o.Constructor, o.Field, baseName)
		fmt.Fprintf(&body, "func %s(fake any) *%s {\n\tvalue := reflect.ValueOf(fake)\n\tmemoize := value.Comparable()\n\tif memoize {\n\t\tif existing, ok := %ss.Load(fake); ok {\n\t\t\treturn existing.(*%s)\n\t\t}\n\t}\n\tif value.Kind() == reflect.Pointer {\n\t\tvalue = value.Elem()\n\t}\n\tfield := value.FieldByName(%q)\n\tif !field.IsValid() || field.Type() != reflect.TypeFor[%s]() {\n\t\tpanic(\"%s: fake has no %s field of type %s\")\n\t}\n\toverlay := &%s{fake: fake, field: field}\n\tif memoize {\n\t\tactual, _ := %ss.LoadOrStore(fake, overlay)\n\t\treturn actual.(*%s)\n\t}\n\treturn overlay\n}\n\n", o.Constructor, o.Type, o.Type, o.Type, o.Field, baseName, o.Constructor, o.Field, baseName, o.Type, o.Type, o.Type)
		fmt.Fprintf(&body, "func (o *%s) base() %s {\n\tbase, _ := o.field.Interface().(%s)\n\treturn base\n}\n\n", o.Type, baseName, baseName)
		fmt.Fprintf(&body, "func (o *%s) overlaidFake() any { return o.fake }\n", o.Type)
		methods := types.NewMethodSet(base)
		var selections []*types.Selection
		for i := 0; i < methods.Len(); i++ {
			if methods.At(i).Obj().Exported() {
				selections = append(selections, methods.At(i))
			}
		}
		sort.Slice(selections, func(i, j int) bool { return selections[i].Obj().Name() < selections[j].Obj().Name() })
		for _, selection := range selections {
			writeMethod(&body, o, selection.Obj().(*types.Func), im)
		}
	}
	var file bytes.Buffer
	file.WriteString("// Code generated by cmd/storeoverlay; DO NOT EDIT.\n\npackage api\n\nimport (\n")
	var std, module []string
	for path := range im.byPath {
		if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
			module = append(module, path)
		} else {
			std = append(std, path)
		}
	}
	sort.Strings(std)
	sort.Strings(module)
	for i, group := range [][]string{std, module} {
		if i > 0 && len(std) > 0 && len(module) > 0 {
			file.WriteString("\n")
		}
		for _, path := range group {
			name := im.byPath[path]
			if name == path[strings.LastIndex(path, "/")+1:] {
				fmt.Fprintf(&file, "\t%q\n", path)
			} else {
				fmt.Fprintf(&file, "\t%s %q\n", name, path)
			}
		}
	}
	file.WriteString(")\n")
	file.Write(body.Bytes())
	return format.Source(file.Bytes())
}

func writeMethod(w io.Writer, o overlay, method *types.Func, im *imports) {
	signature := method.Type().(*types.Signature)
	var params, args, paramTypes []string
	for i := 0; i < signature.Params().Len(); i++ {
		typ := types.TypeString(signature.Params().At(i).Type(), im.qualify)
		name := fmt.Sprintf("a%d", i)
		arg := name
		if signature.Variadic() && i == signature.Params().Len()-1 {
			typ = "..." + strings.TrimPrefix(typ, "[]")
			arg += "..."
		}
		params = append(params, name+" "+typ)
		args = append(args, arg)
		paramTypes = append(paramTypes, typ)
	}
	var results []string
	for i := 0; i < signature.Results().Len(); i++ {
		results = append(results, types.TypeString(signature.Results().At(i).Type(), im.qualify))
	}
	result := strings.Join(results, ", ")
	if len(results) > 1 {
		result = "(" + result + ")"
	}
	name := method.Name()
	call := fmt.Sprintf("%s(%s)", name, strings.Join(args, ", "))
	fmt.Fprintf(w, "\nfunc (o *%s) %s(%s) %s {\n", o.Type, name, strings.Join(params, ", "), result)
	fmt.Fprintf(w, "\tif fake, ok := o.fake.(interface{ %s(%s) %s }); ok {\n", name, strings.Join(paramTypes, ", "), result)
	if len(results) == 0 {
		fmt.Fprintf(w, "\t\tfake.%s\n\t\treturn\n\t}\n\to.base().%s\n}\n", call, call)
		return
	}
	fmt.Fprintf(w, "\t\treturn fake.%s\n\t}\n\treturn o.base().%s\n}\n", call, call)
}
