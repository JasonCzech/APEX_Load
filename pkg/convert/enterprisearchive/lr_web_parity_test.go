package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/clienttimestamps"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/paritytest"
	"github.com/jasonczech/apexload/pkg/recorder"
)

// TestLRWebParityCorpus imports every archive under testdata/lr_web_parity,
// replays it once against a recording fixture server, and compares the wire
// traffic with the case's expected.json (or its LoadRunner replay.har when one
// is present). Gated cases must instead carry their execution gate and be
// refused. See pkg/internal/paritytest for the case format.
func TestLRWebParityCorpus(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "lr_web_parity", "*"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirs)
	ran := 0
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		ran++
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			exp, err := paritytest.LoadExpectation(dir)
			if err != nil {
				t.Fatal(err)
			}
			server := paritytest.NewServer(exp.Responses)
			defer server.Close()

			archive := zipParityArchive(t, dir, server)
			result, err := ConvertZip(archive, ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			scripts := result.effectiveScripts()
			if len(scripts) == 0 && exp.Outcome == paritytest.OutcomeGated {
				// A review-only import generates no script at all; its review
				// artifact must still name the reason.
				checkParityReviewArtifact(t, result, exp)
				if recorded := server.Recorded(); len(recorded) > 0 {
					t.Errorf("a review-only import sent %d request(s)", len(recorded))
				}
				return
			}
			if len(scripts) != 1 {
				t.Fatalf("converted %d scripts, want 1", len(scripts))
			}
			for _, problem := range paritytest.CheckOutcome(context.Background(), server, exp, scripts[0], result.Warnings) {
				t.Error(problem)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no parity corpus cases found")
	}
}

// checkParityReviewArtifact verifies that a review-only import's artifact
// carries every expected gate reason code.
func checkParityReviewArtifact(t *testing.T, result *ConvertResult, exp *paritytest.Expectation) {
	t.Helper()
	if result.ReviewArtifact == nil {
		t.Fatal("no script and no review artifact were produced")
	}
	raw, err := json.Marshal(result.ReviewArtifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range exp.GateReasonCodes {
		if !strings.Contains(string(raw), `"`+code+`"`) {
			t.Errorf("review artifact does not name reason code %q: %s", code, raw)
		}
	}
	joined := strings.Join(result.Warnings, "\n")
	for _, fragment := range exp.WarningsContain {
		if !strings.Contains(joined, fragment) {
			t.Errorf("warnings do not mention %q; got:\n%s", fragment, joined)
		}
	}
}

// zipParityArchive zips a corpus case's source files, pointing every
// placeholder origin at the fixture server.
func zipParityArchive(t *testing.T, dir string, server *paritytest.Server) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch rel {
		case "expected.json", "replay.har", "README.md":
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if utf8.Valid(content) {
			content = []byte(server.Expand(string(content)))
		}
		file, err := writer.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = file.Write(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestLRWebParityJavaScriptTwins qualifies representative C corpus cases
// against their VuGen JavaScript (Action.js with web.* and lr.* calls) twins,
// verifying that both convert and replay with the same wire requests and
// outcome against the case's expected.json.
func TestLRWebParityJavaScriptTwins(t *testing.T) {
	twins := []struct {
		name       string
		actionJS   string
		extraFiles map[string]string
		dropFiles  []string
	}{
		{
			name: "url_mode_login_flow",
			actionJS: `function Action()
{
	web.regSaveParamEx({
		paramName: 'SessionID',
		lb: 'name="session" value="',
		rb: '"',
		searchFilters: { scope: 'Body' }
	});

	web.url({
		name: 'home',
		url: 'http://parity.test/app/home',
		resource: 0,
		recContentType: 'text/html',
		mode: 'HTTP'
	});

	lr.startTransaction('Login');

	web.submitData({
		name: 'login',
		action: 'http://parity.test/app/login',
		method: 'POST',
		recContentType: 'text/html',
		mode: 'HTTP',
		itemData: [
			{ name: 'user', value: 'jojo' },
			{ name: 'session', value: '{SessionID}' }
		]
	});

	lr.endTransaction('Login', lr.AUTO);

	lr.thinkTime(2);

	web.customRequest({
		name: 'orders',
		url: 'http://parity.test/api/orders?session={SessionID}',
		method: 'POST',
		encType: 'application/json',
		body: '{"item":"widget","qty":2}'
	});

	return 0;
}
`,
		},
		{
			name: "referer_header",
			actionJS: `function Action()
{
	web.url({
		name: 'home',
		url: 'http://parity.test/app/home',
		resource: 0,
		referer: '',
		mode: 'HTTP'
	});

	web.submitData({
		name: 'search',
		action: 'http://parity.test/app/search',
		method: 'POST',
		referer: 'http://parity.test/app/home',
		mode: 'HTTP',
		itemData: [
			{ name: 'q', value: 'widgets' }
		]
	});

	web.customRequest({
		name: 'results',
		url: 'http://parity.test/api/results',
		method: 'GET',
		referer: 'http://parity.test/app/search?q=widgets'
	});

	return 0;
}
`,
		},
		{
			name: "cookie_injection",
			actionJS: `function Action()
{
	web.addCookie('consent=yes');
	web.addCookie('sid=injected');

	web.url({
		name: 'home',
		url: 'http://parity.test/app/home',
		resource: 0
	});

	web.url({
		name: 'account',
		url: 'http://parity.test/app/account',
		resource: 0
	});

	web.removeCookie('consent');

	web.url({
		name: 'final',
		url: 'http://parity.test/app/final',
		resource: 0
	});

	return 0;
}
`,
		},
		{
			name: "web_set_user_host_scope",
			actionJS: `function Action()
{
	web.setUser('alice', 's3cret', 'http://parity.test');

	web.url({
		name: 'secure',
		url: 'http://parity.test/secure/home',
		resource: 0
	});

	web.url({
		name: 'elsewhere',
		url: 'http://alias.parity.test/public/asset',
		resource: 0
	});

	return 0;
}
`,
		},
		{
			name: "body_variants",
			actionJS: `function Action()
{
	lr.saveString('a\\x41', 'Token');

	web.customRequest({
		name: 'upload_binary',
		url: 'http://parity.test/api/upload',
		method: 'POST',
		encType: 'application/octet-stream',
		bodyBinary: '\\x00\\x01ABC\\xFF'
	});

	web.customRequest({
		name: 'upload_binary_param',
		url: 'http://parity.test/api/upload',
		method: 'POST',
		encType: 'application/octet-stream',
		bodyBinary: '\\x02{Token}\\x03'
	});

	web.customRequest({
		name: 'upload_file',
		url: 'http://parity.test/api/upload',
		method: 'POST',
		encType: 'application/json',
		bodyFilePath: 'payload.json'
	});

	web.customRequest({
		name: 'upload_unicode',
		url: 'http://parity.test/api/upload',
		method: 'POST',
		encType: 'text/plain',
		bodyUnicode: 'hello {Token}'
	});

	return 0;
}
`,
		},
		{
			name: "multipart_upload",
			actionJS: `function Action()
{
	lr.saveString('Q3', 'Quarter');

	web.submitData({
		name: 'upload',
		action: 'http://parity.test/upload',
		method: 'POST',
		encType: 'multipart/form-data',
		recContentType: 'application/json',
		mode: 'HTTP',
		itemData: [
			{ name: 'description', value: 'quarterly report {Quarter}' },
			{ name: 'file', value: 'report.txt', file: true, contentType: 'text/plain' },
			{ name: 'logo', value: 'logo.bin', file: 'Yes', contentType: 'application/octet-stream' },
			{ name: 'summary', value: 'summary.txt', filePath: 'report.txt', file: true, contentType: 'text/plain' }
		]
	});

	return 0;
}
`,
		},
		{
			name: "resource_steps",
			actionJS: `function Action()
{
	web.url({
		name: 'logo.png',
		url: 'http://parity.test/static/missing.png',
		resource: 1,
		recContentType: 'image/png',
		mode: 'HTTP'
	});

	web.url({
		name: 'home',
		url: 'http://parity.test/app/home',
		resource: 0,
		recContentType: 'text/html',
		mode: 'HTML'
	});

	return 0;
}
`,
		},
		{
			name: "async_poll",
			actionJS: `function Action()
{
	web.regAsyncAttributes({
		id: 'Poll_0',
		pattern: 'Poll',
		url: 'http://parity.test/api/status',
		pollIntervalMs: 200,
		requestCB: 'Poll_0_RequestCB',
		responseBodyBufferCB: 'Poll_0_ResponseBodyBufferCB',
		responseCB: 'Poll_0_ResponseCB'
	});

	web.regSaveParamJson({
		paramName: 'JobStatus',
		queryString: '$.status',
		notFound: 'warning',
		searchFilters: { scope: 'BODY' }
	});

	web.url({
		name: 'status',
		url: 'http://parity.test/api/status',
		resource: 0,
		recContentType: 'application/json',
		mode: 'HTTP'
	});

	web.sync({
		paramValueEquals: 'JobStatus=done',
		timeout: 10
	});

	web.stopAsync({
		id: 'Poll_0'
	});

	web.url({
		name: 'result',
		url: 'http://parity.test/app/result?job={JobStatus}&seen={PollSeen}',
		resource: 0,
		recContentType: 'text/html',
		mode: 'HTTP'
	});

	return 0;
}
`,
			dropFiles: []string{"AsyncCallbacks.c"},
			extraFiles: map[string]string{
				"AsyncCallbacks.js": `function Poll_0_RequestCB()
{
	return WEB_ASYNC_CB_RC_OK;
}

function Poll_0_ResponseBodyBufferCB(aLastBufferStr, aLastBufferLen, aAccumulatedStr, aAccumulatedLen, aHttpStatusCode)
{
	return WEB_ASYNC_CB_RC_OK;
}

function Poll_0_ResponseCB(aResponseHeadersStr, aResponseHeadersLen, aResponseBodyStr, aResponseBodyLen, aHttpStatusCode)
{
	lr.saveString('yes', 'PollSeen');
	return WEB_ASYNC_CB_RC_OK;
}
`,
			},
		},
		{
			name: "websocket_chat_after_login",
			actionJS: `function Action()
{
	web.submitData({
		name: 'login',
		action: 'http://parity.test/login',
		method: 'POST',
		recContentType: 'text/html',
		mode: 'HTTP',
		itemData: [
			{ name: 'user', value: 'ada' }
		]
	});

	web.webSocketConnect({
		id: '0',
		uri: 'ws://parity.test/chat',
		origin: 'http://parity.test',
		onOpenCB: 'OnOpenCB0',
		onMessageCB: 'OnMessageCB0',
		onErrorCB: 'OnErrorCB0',
		onCloseCB: 'OnCloseCB0'
	});

	web.webSocketSend({
		id: '0',
		buffer: '{"type":"say","text":"hello"}',
		isBinary: 0
	});

	web.sync({
		paramExists: 'LastMessage',
		timeout: 10
	});

	web.url({
		name: 'history',
		url: 'http://parity.test/history?last={LastMessage}',
		resource: 0,
		recContentType: 'text/html',
		mode: 'HTTP'
	});

	web.webSocketClose({
		id: '0',
		code: 1000,
		reason: 'bye'
	});

	return 0;
}
`,
			dropFiles: []string{"WebSocketCallbacks.c"},
			extraFiles: map[string]string{
				"WebSocketCallbacks.js": `function OnOpenCB0(connectionID, responseHeader, length)
{
	lr.outputMessage('WebSocket ID = %s connected successfully, first buffer length = %d', connectionID, length);
}

function OnMessageCB0(connectionID, isbinary, data, length)
{
	lr.outputMessage('WebSocket ID = %s got message=%s length = %d', connectionID, data, length);
	lr.saveString(data, 'LastMessage');
}

function OnErrorCB0(connectionID, message, length)
{
	lr.outputMessage('WebSocket ID = %s error occurred. Error message = %s', connectionID, message);
}

function OnCloseCB0(connectionID, isClosedByClient, code, reason, length)
{
	lr.outputMessage('WebSocket ID = %s closed. close code = %d and reason = %s', connectionID, code, reason);
}
`,
			},
		},
	}

	for _, tc := range twins {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join("testdata", "lr_web_parity", tc.name)
			exp, err := paritytest.LoadExpectation(dir)
			if err != nil {
				t.Fatal(err)
			}

			cServer := paritytest.NewServer(exp.Responses)
			defer cServer.Close()
			cArchive := zipParityArchive(t, dir, cServer)
			cResult, err := ConvertZip(cArchive, ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatalf("C ConvertZip: %v", err)
			}
			for _, problem := range paritytest.CheckOutcome(context.Background(), cServer, exp, cResult.Script, cResult.Warnings) {
				t.Errorf("C twin: %s", problem)
			}

			jsServer := paritytest.NewServer(exp.Responses)
			defer jsServer.Close()
			jsArchive := zipJavaScriptTwinArchive(t, dir, jsServer, tc.actionJS, tc.extraFiles, tc.dropFiles)
			jsResult, err := ConvertZip(jsArchive, ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatalf("JS ConvertZip: %v", err)
			}
			if jsResult.DetectedProtocol == nil || jsResult.DetectedProtocol.DisplayName != "Web - HTTP/HTML (JavaScript)" {
				t.Fatalf("DetectedProtocol = %+v, want Web - HTTP/HTML (JavaScript)", jsResult.DetectedProtocol)
			}
			for _, problem := range paritytest.CheckOutcome(context.Background(), jsServer, exp, jsResult.Script, jsResult.Warnings) {
				t.Errorf("JS twin: %s", problem)
			}

			// Verify both C and JS twins produced equivalent wire request sequences
			// (accounting for background poll extra repeats on known poll endpoints).
			var pollPaths []string
			if tc.name == "async_poll" {
				pollPaths = []string{"/api/status"}
			}
			cRec := normalizePollRepeats(cServer.Recorded(), pollPaths...)
			jsRec := normalizePollRepeats(jsServer.Recorded(), pollPaths...)
			if len(cRec) != len(jsRec) {
				t.Fatalf("recorded request count mismatch: C=%d JS=%d", len(cRec), len(jsRec))
			}
			for i := range cRec {
				if cRec[i].Method != jsRec[i].Method || cRec[i].Path != jsRec[i].Path || cRec[i].Query != jsRec[i].Query {
					t.Errorf("request %d mismatch: C=%s %s?%s vs JS=%s %s?%s",
						i+1, cRec[i].Method, cRec[i].Path, cRec[i].Query, jsRec[i].Method, jsRec[i].Path, jsRec[i].Query)
				}
			}
		})
	}
}

func zipJavaScriptTwinArchive(
	t *testing.T,
	dir string,
	server *paritytest.Server,
	actionJS string,
	extraFiles map[string]string,
	dropFiles []string,
) []byte {
	t.Helper()
	dropSet := make(map[string]bool, len(dropFiles))
	for _, name := range dropFiles {
		dropSet[name] = true
	}
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch rel {
		case "expected.json", "replay.har", "README.md", "Action.c":
			return nil
		}
		if dropSet[rel] {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(strings.ToLower(rel), ".usr") {
			usrText := strings.ReplaceAll(string(content), "Action.c", "Action.js")
			if !strings.Contains(strings.ToLower(usrText), "scriptlanguage=") {
				usrText = strings.Replace(usrText, "[General]\n", "[General]\nScriptLanguage=JavaScript\n", 1)
			}
			content = []byte(usrText)
		}
		if utf8.Valid(content) {
			content = []byte(server.Expand(string(content)))
		}
		file, err := writer.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = file.Write(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	actionEntry, err := writer.Create("Action.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := actionEntry.Write([]byte(server.Expand(actionJS))); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(extraFiles))
	for name := range extraFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(server.Expand(extraFiles[name]))); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func normalizePollRepeats(recorded []paritytest.Recorded, pollPaths ...string) []paritytest.Recorded {
	allowed := make(map[string]bool, len(pollPaths))
	for _, p := range pollPaths {
		allowed[p] = true
	}
	var out []paritytest.Recorded
	for _, req := range recorded {
		if len(out) > 0 && allowed[req.Path] {
			prev := out[len(out)-1]
			if prev.Method == req.Method && prev.Path == req.Path && prev.Query == req.Query && bytes.Equal(prev.Body, req.Body) {
				continue
			}
		}
		out = append(out, req)
	}
	return out
}

// TestLRWebParityEquivalenceWithHARAndProxyCaptures verifies that LoadRunner
// URL-level web scripts and HAR/proxy captures representing the same HTTP
// conversation produce equivalent observable wire requests on replay, while
// the LoadRunner import preserves its authored transactions, phases,
// loops/conditions, concurrent groups, parameter allocation, and review gates.
func TestLRWebParityEquivalenceWithHARAndProxyCaptures(t *testing.T) {
	t.Run("hidden_field_login_lr_vs_har_vs_proxy", func(t *testing.T) {
		harDir := filepath.Join("..", "har", "testdata", "native_parity", "hidden_field_login")
		exp, err := paritytest.LoadExpectation(harDir)
		if err != nil {
			t.Fatal(err)
		}

		// 1. Convert and replay HAR capture.
		harServer := paritytest.NewServer(exp.Responses)
		defer harServer.Close()
		harRaw, err := os.ReadFile(filepath.Join(harDir, "recording.har"))
		if err != nil {
			t.Fatal(err)
		}
		harExpanded := []byte(harServer.Expand(string(harRaw)))
		harResult, err := har.Convert(harExpanded, har.ConvertOptions{})
		if err != nil {
			t.Fatalf("har.Convert: %v", err)
		}
		evidence, err := clienttimestamps.FromHAR(harExpanded)
		if err != nil {
			t.Fatalf("clienttimestamps.FromHAR: %v", err)
		}
		if report := clienttimestamps.Apply(harResult.Script, evidence); report.Replaced < 0 {
			t.Fatalf("unexpected clienttimestamps.Apply report: %+v", report)
		}
		for _, problem := range paritytest.CheckOutcome(context.Background(), harServer, exp, harResult.Script, harResult.Warnings) {
			t.Errorf("HAR capture: %s", problem)
		}

		// 2. Convert and replay Proxy session capture.
		proxyDir := filepath.Join("..", "..", "recorder", "testdata", "native_parity", "hidden_field_login")
		proxyServer := paritytest.NewServer(exp.Responses)
		defer proxyServer.Close()
		proxyRaw, err := os.ReadFile(filepath.Join(proxyDir, "session.json"))
		if err != nil {
			t.Fatal(err)
		}
		proxyExpanded := proxyServer.Expand(string(proxyRaw))
		proxyExpanded = strings.ReplaceAll(proxyExpanded, "parity.test", strings.TrimPrefix(proxyServer.URL, "http://"))
		var session recorder.Session
		if err := json.Unmarshal([]byte(proxyExpanded), &session); err != nil {
			t.Fatalf("session.json: %v", err)
		}
		for i := range session.Requests {
			req := &session.Requests[i]
			req.RawHeaders = req.Headers
			req.RawResponseHeaders = req.ResponseHeaders
		}
		proxyResult := recorder.ConvertSessionWithOptions(&session, session.Name, recorder.ConvertOptions{})
		for _, problem := range paritytest.CheckOutcome(context.Background(), proxyServer, exp, proxyResult.Script, proxyResult.Warnings) {
			t.Errorf("Proxy capture: %s", problem)
		}

		// 3. Convert and replay equivalent LoadRunner URL-level web script with
		// authored transactions, phases, control flow, concurrent group, and .prm
		// parameter allocation.
		lrServer := paritytest.NewServer(exp.Responses)
		defer lrServer.Close()
		lrFiles := map[string]string{
			"Login.usr":   "[General]\nType=Multi\nDefaultCfg=default.cfg\nParameterFile=Login.prm\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n[RunLogic]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n",
			"default.cfg": "[General]\n[WEB]\nKeepAlive=Yes\n",
			"Login.prm":   "[parameter:User]\nType=\"Table\"\nTable=\"users.dat\"\nColumnName=\"user\"\nSelectNextRow=\"Unique\"\nGenerateNewVal=\"EachIteration\"\nOutOfValues=\"ContinueWithLast\"\nAutoAllocateBlockSize=\"1\"\n",
			"users.dat":   "user\njojo\n",
			"vuser_init.c": `vuser_init()
{
	lr_save_string("1", "ShouldLogin");
	return 0;
}
`,
			"Action.c": lrServer.Expand(`Action()
{
	int i;
	web_reg_save_param_ex("ParamName=csrf_token",
		"LB=name=\"csrf_token\" value=\"",
		"RB=\"",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_concurrent_start(NULL);
	web_url("home",
		"URL=http://parity.test/app/home",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTTP",
		LAST);
	web_concurrent_end(NULL);

	for (i = 0; i < 1; i++) {
		if (atoi(lr_eval_string("{ShouldLogin}")) == 1) {
			lr_start_transaction("Auth_Login");
			web_submit_data("login",
				"Action=http://parity.test/app/login",
				"Method=POST",
				"Referer=http://parity.test/app/home",
				"RecContentType=text/html",
				"Mode=HTTP",
				ITEMDATA,
				"Name=csrf_token", "Value={csrf_token}", ENDITEM,
				"Name=user", "Value={User}", ENDITEM,
				LAST);
			lr_end_transaction("Auth_Login", LR_AUTO);
		}
	}

	return 0;
}
`),
			"vuser_end.c": `vuser_end()
{
	lr_output_message("done");
	return 0;
}
`,
		}
		lrArchive, err := zipArchiveFiles(lrFiles)
		if err != nil {
			t.Fatal(err)
		}
		lrResult, err := ConvertZip(lrArchive, ConvertOptions{PreserveTransactions: true})
		if err != nil {
			t.Fatalf("LR ConvertZip: %v", err)
		}
		for _, problem := range paritytest.CheckOutcome(context.Background(), lrServer, exp, lrResult.Script, lrResult.Warnings) {
			t.Errorf("LR archive: %s", problem)
		}

		// Verify wire equivalence across all 3 pipelines (LR, HAR, Proxy).
		assertEquivalentWireTraffic(t, "LR vs HAR", lrServer.Recorded(), harServer.Recorded())
		assertEquivalentWireTraffic(t, "LR vs Proxy", lrServer.Recorded(), proxyServer.Recorded())

		// Verify LoadRunner-specific authored constructs were preserved on the DSL script.
		script := lrResult.Script
		if len(script.Init) == 0 || len(script.Teardown) == 0 {
			t.Fatalf("expected vuser_init and vuser_end phases to be preserved, got init=%d teardown=%d", len(script.Init), len(script.Teardown))
		}
		if len(script.Parameters) != 1 || script.Parameters[0].Name != "user" || script.Parameters[0].Selection != "unique" || !script.Parameters[0].AutoAllocate {
			t.Fatalf("expected Unique auto-allocated parameter to be preserved, got %+v", script.Parameters)
		}
		assertScriptContainsConstructs(t, script, []string{"http_sequence", "control_loop", "control_if", "source_transaction"})
		hasConcurrentSeq := false
		for _, a := range script.Actions {
			if a.Type == "http_sequence" && a.Params["execution_mode"] == dsl.HTTPSequenceConcurrent {
				hasConcurrentSeq = true
				break
			}
		}
		if !hasConcurrentSeq {
			t.Fatalf("expected web_concurrent_start/end to be preserved as concurrent http_sequence")
		}
	})

	t.Run("redirect_login_lr_vs_har_and_review_gates", func(t *testing.T) {
		harDir := filepath.Join("..", "har", "testdata", "native_parity", "redirect_login")
		exp, err := paritytest.LoadExpectation(harDir)
		if err != nil {
			t.Fatal(err)
		}

		harServer := paritytest.NewServer(exp.Responses)
		defer harServer.Close()
		harRaw, err := os.ReadFile(filepath.Join(harDir, "recording.har"))
		if err != nil {
			t.Fatal(err)
		}
		harExpanded := []byte(harServer.Expand(string(harRaw)))
		harResult, err := har.Convert(harExpanded, har.ConvertOptions{})
		if err != nil {
			t.Fatalf("har.Convert: %v", err)
		}
		for _, problem := range paritytest.CheckOutcome(context.Background(), harServer, exp, harResult.Script, harResult.Warnings) {
			t.Errorf("HAR capture: %s", problem)
		}

		lrServer := paritytest.NewServer(exp.Responses)
		defer lrServer.Close()
		lrFiles := map[string]string{
			"Redirect.usr": "[General]\nType=Multi\nDefaultCfg=default.cfg\n[Actions]\nAction=Action.c\n",
			"default.cfg":  "[General]\n",
			"Action.c": lrServer.Expand(`Action()
{
	web_reg_save_param_ex("ParamName=SsoCode",
		"LB=code=",
		"RB=&state=",
		SEARCH_FILTERS,
		"Scope=Headers",
		LAST);

	web_submit_data("login",
		"Action=http://parity.test/app/login",
		"Method=POST",
		"Mode=HTTP",
		ITEMDATA,
		"Name=user", "Value=jojo", ENDITEM,
		"Name=pass", "Value=bean", ENDITEM,
		LAST);

	lr_output_message("code=%s", lr_eval_string("{SsoCode}"));

	return 0;
}
`),
		}
		lrArchive, err := zipArchiveFiles(lrFiles)
		if err != nil {
			t.Fatal(err)
		}
		lrResult, err := ConvertZip(lrArchive, ConvertOptions{})
		if err != nil {
			t.Fatalf("LR ConvertZip: %v", err)
		}
		for _, problem := range paritytest.CheckOutcome(context.Background(), lrServer, exp, lrResult.Script, lrResult.Warnings) {
			t.Errorf("LR archive: %s", problem)
		}

		assertEquivalentWireTraffic(t, "Redirect LR vs HAR", lrServer.Recorded(), harServer.Recorded())

		// Also verify that an unsupported LoadRunner construct (HTML-mode navigation
		// targeting a sub-frame) preserves its durable execution review gate rather than
		// silently dropping the step.
		gatedFiles := map[string]string{
			"Gated.usr":   "[General]\nType=Multi\nDefaultCfg=default.cfg\n[Actions]\nAction=Action.c\n",
			"default.cfg": "[General]\n",
			"Action.c": `Action()
{
	web_url("home", "URL=http://parity.test/app/home", "Mode=HTML", LAST);
	web_link("Continue", "Text=Continue", "Frame=body", LAST);
	return 0;
}
`,
		}
		gatedArchive, err := zipArchiveFiles(gatedFiles)
		if err != nil {
			t.Fatal(err)
		}
		gatedResult, err := ConvertZip(gatedArchive, ConvertOptions{})
		if err != nil {
			t.Fatalf("gated ConvertZip: %v", err)
		}
		rawScript, err := json.Marshal(gatedResult.Script)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(rawScript), "html_navigation_requires_page_context") {
			t.Fatalf("expected durable html_navigation_requires_page_context review gate, got %s", rawScript)
		}
	})
}

func assertEquivalentWireTraffic(t *testing.T, label string, left, right []paritytest.Recorded) {
	t.Helper()
	if len(left) != len(right) {
		t.Fatalf("%s: request count mismatch (%d vs %d)", label, len(left), len(right))
	}
	for i := range left {
		if left[i].Method != right[i].Method || left[i].Path != right[i].Path || left[i].Query != right[i].Query || !bytes.Equal(left[i].Body, right[i].Body) {
			t.Errorf("%s request %d mismatch: (%s %s?%s body=%q) vs (%s %s?%s body=%q)",
				label, i+1,
				left[i].Method, left[i].Path, left[i].Query, string(left[i].Body),
				right[i].Method, right[i].Path, right[i].Query, string(right[i].Body),
			)
		}
	}
}

func assertScriptContainsConstructs(t *testing.T, script *dsl.Script, wantTypes []string) {
	t.Helper()
	seen := map[string]bool{}
	var walk func(actions []dsl.Action)
	walk = func(actions []dsl.Action) {
		for _, a := range actions {
			seen[a.Type] = true
			if a.Control != nil {
				walk(a.Control.Steps)
				walk(a.Control.Else)
				walk(a.Control.Default)
				for _, branch := range a.Control.Branches {
					walk(branch.Steps)
				}
				for _, c := range a.Control.Cases {
					walk(c.Steps)
				}
			}
		}
	}
	walk(script.Init)
	walk(script.Actions)
	walk(script.Teardown)
	for _, want := range wantTypes {
		if !seen[want] {
			t.Errorf("expected script to preserve %q construct, saw %v", want, seen)
		}
	}
}
