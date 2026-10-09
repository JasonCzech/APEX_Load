package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// TestGeneratedJavaScriptTwinsImportLikeTheirCOriginals writes the
// JavaScript form of every C corpus case whose action file is a list of
// calls (jsTwinFromC) and requires its import to equal the C original's.
// The hand-written twins under lr_web_parity_js_twins cover JavaScript
// layout; this covers the attribute surface of the whole corpus.
func TestGeneratedJavaScriptTwinsImportLikeTheirCOriginals(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "lr_web_parity", "*"))
	if err != nil {
		t.Fatal(err)
	}
	converted := 0
	for _, dir := range dirs {
		action, err := os.ReadFile(filepath.Join(dir, "Action.c"))
		if err != nil {
			continue
		}
		name := filepath.Base(dir)
		twin, reason := jsTwinFromC(string(action))
		if reason != "" {
			t.Logf("%s: no generated twin (%s)", name, reason)
			continue
		}
		converted++
		t.Run(name, func(t *testing.T) {
			original, err := ConvertZip(zipDirFiles(t, dir, nil), ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatal(err)
			}
			jsResult, err := ConvertZip(javaScriptTwinArchive(t, dir, twin), ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatal(err)
			}
			if (original.Script == nil) != (jsResult.Script == nil) {
				t.Fatalf("one form imported and the other did not\njs: %v\nc: %v\ntwin:\n%s", jsResult.Warnings, original.Warnings, twin)
			}
			if original.Script == nil {
				return
			}
			if got, want := comparableScript(t, jsResult.Script), comparableScript(t, original.Script); got != want {
				t.Fatalf("the JavaScript twin imports differently\ntwin:\n%s\njs: %s\nc:  %s", twin, got, want)
			}
		})
	}
	if converted < 12 {
		t.Fatalf("only %d corpus cases had a generated twin", converted)
	}
}

// javaScriptTwinArchive is the case's archive with Action.c replaced by
// Action.js and the .usr declaring JavaScript.
func javaScriptTwinArchive(t *testing.T, dir, twin string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"Action.js": []byte(twin)}
	for _, entry := range entries {
		switch name := entry.Name(); {
		case entry.IsDir(), name == "expected.json", name == "Action.c":
		case strings.HasSuffix(name, ".usr"):
			content, _ := os.ReadFile(filepath.Join(dir, name))
			usr := strings.Replace(string(content), "Action.c", "Action.js", 1)
			usr = strings.Replace(usr, "[General]", "[General]\nScriptLanguage=JavaScript", 1)
			files[name] = []byte(usr)
		default:
			content, _ := os.ReadFile(filepath.Join(dir, name))
			files[name] = content
		}
	}
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, _ := writer.Create(name)
		_, _ = w.Write(files[name])
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var cCallStatement = regexp.MustCompile(`(?s)^\s*([a-z_]+)\s*\((.*)\)\s*$`)

// jsTwinFromC writes the JavaScript form of a C action file made of calls,
// the way VuGen writes a JavaScript script.
func jsTwinFromC(source string) (string, string) {
	body := blankCComments(source)
	open, close := strings.Index(body, "{"), strings.LastIndex(body, "}")
	if open < 0 || close < open {
		return "", "no function body"
	}
	statements, reason := splitSourceCallbackStatements(body[open+1 : close])
	if reason != "" {
		return "", reason
	}
	var out strings.Builder
	out.WriteString("function Action()\n{\n")
	for _, statement := range statements {
		if statement == "return 0" {
			continue
		}
		match := cCallStatement.FindStringSubmatch(statement)
		if match == nil {
			return "", "a statement that is not a call: " + statement
		}
		call, reason := jsTwinCall(match[1], splitCFunctionArgs(match[2]))
		if reason != "" {
			return "", reason
		}
		out.WriteString("\t" + call + ";\n")
	}
	out.WriteString("\treturn 0;\n}\n")
	return out.String(), ""
}

func jsTwinCall(function string, args []string) (string, string) {
	object, method, ok := strings.Cut(function, "_")
	if !ok || (object != "web" && object != "lr") {
		return "", "the call " + function
	}
	jsName := object + "." + camelCase(method)
	if object == "web" && len(args) > 0 && strings.TrimSpace(args[len(args)-1]) == "LAST" {
		return jsTwinObjectCall(function, jsName, args[:len(args)-1])
	}
	var jsArgs []string
	for _, arg := range args {
		value, reason := jsTwinValue(arg)
		if reason != "" {
			return "", reason
		}
		jsArgs = append(jsArgs, value)
	}
	return jsName + "(" + strings.Join(jsArgs, ", ") + ")", ""
}

// jsTwinObjectCall writes web.<fn>({...}) from a C attribute list.
func jsTwinObjectCall(function, jsName string, args []string) (string, string) {
	var properties []string
	var list []string
	var item []string
	listName := ""
	filters := []string(nil)
	inFilters := false
	for index, raw := range args {
		arg := strings.TrimSpace(raw)
		switch arg {
		case "ITEMDATA", "EXTRARES":
			listName = map[string]string{"ITEMDATA": "itemData", "EXTRARES": "extraRes"}[arg]
			continue
		case "ENDITEM":
			list = append(list, "{ "+strings.Join(item, ", ")+" }")
			item = nil
			continue
		case "SEARCH_FILTERS":
			inFilters = true
			continue
		case "STARTHIDDENS", "ENDHIDDENS", "BEGIN_ARGUMENTS", "END_ARGUMENTS":
			return "", "the C marker " + arg
		}
		literal, ok := cStringValue(arg)
		if !ok {
			return "", "the argument " + arg
		}
		if index == 0 && jsStepFunctions[function] {
			properties = append(properties, "name : "+jsQuote(literal))
			continue
		}
		key, value, ok := strings.Cut(literal, "=")
		if !ok {
			return "", "the positional argument " + arg
		}
		property := jsTwinKey(key) + " : " + jsQuote(value)
		switch {
		case listName != "":
			item = append(item, property)
		case inFilters:
			filters = append(filters, property)
		default:
			properties = append(properties, property)
		}
	}
	if listName != "" {
		if len(item) > 0 {
			return "", "an unterminated item list"
		}
		properties = append(properties, listName+" : [ "+strings.Join(list, ", ")+" ]")
	}
	if len(filters) > 0 {
		properties = append(properties, "searchFilters : { "+strings.Join(filters, ", ")+" }")
	}
	return jsName + "({ " + strings.Join(properties, ", ") + " })", ""
}

func jsTwinValue(arg string) (string, string) {
	arg = strings.TrimSpace(arg)
	if literal, ok := cStringValue(arg); ok {
		return jsQuote(literal), ""
	}
	switch arg {
	case "LR_AUTO", "LR_PASS", "LR_FAIL", "LR_STOP":
		return "lr." + strings.TrimPrefix(arg, "LR_"), ""
	}
	if strings.Trim(arg, "0123456789.") == "" && arg != "" {
		return arg, ""
	}
	return "", "the argument " + arg
}

// cStringValue decodes a C string literal, including adjacent literals.
func cStringValue(arg string) (string, bool) {
	arg = strings.TrimSpace(arg)
	if !strings.HasPrefix(arg, `"`) || !strings.HasSuffix(arg, `"`) {
		return "", false
	}
	var out strings.Builder
	inString, escaped := false, false
	for _, r := range arg {
		switch {
		case !inString && r == '"':
			inString = true
		case !inString && unicode.IsSpace(r):
		case !inString:
			return "", false
		case escaped:
			switch r {
			case 'n':
				out.WriteRune('\n')
			case 'r':
				out.WriteRune('\r')
			case 't':
				out.WriteRune('\t')
			default:
				out.WriteRune(r)
			}
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inString = false
		default:
			out.WriteRune(r)
		}
	}
	return out.String(), !inString
}

func jsTwinKey(attribute string) string {
	for js, c := range jsAttributeNames {
		if c == attribute {
			return js
		}
	}
	runes := []rune(attribute)
	runes[0] = unicode.ToLower(runes[0])
	key := string(runes)
	if !jsIdentifier.MatchString(key) {
		// Text/IC and other flagged attributes are quoted keys.
		return jsQuote(key)
	}
	return key
}

var jsIdentifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func jsQuote(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return "'" + replacer.Replace(text) + "'"
}

func camelCase(snake string) string {
	parts := strings.Split(snake, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
