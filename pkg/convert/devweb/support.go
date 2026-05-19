package devweb

type SupportMatrixEntry struct {
	ID              string   `json:"id"`
	Area            string   `json:"area"`
	Construct       string   `json:"construct"`
	Status          string   `json:"status"`
	ExecutionStatus string   `json:"execution_status"`
	ReviewRequired  bool     `json:"review_required"`
	Fields          []string `json:"fields,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

type TestedExample struct {
	Title    string `json:"title"`
	Coverage string `json:"coverage"`
	Test     string `json:"test"`
}

func GetSupportMatrix() []SupportMatrixEntry {
	return []SupportMatrixEntry{
		{
			ID:              "devweb-http-webrequest",
			Area:            "HTTP",
			Construct:       "load.WebRequest({...}).send()",
			Status:          "supported",
			ExecutionStatus: "executable",
			Fields:          []string{"url", "method", "headers", "body", "bodyText"},
			Notes:           "Converted into Stormrunner HTTP request actions with header/body preservation.",
		},
		{
			ID:              "devweb-think-time",
			Area:            "Runtime",
			Construct:       "load.thinkTime(seconds)",
			Status:          "supported",
			ExecutionStatus: "executable",
			Fields:          []string{"seconds"},
			Notes:           "Attached as post-action fixed think time on the preceding converted action.",
		},
		{
			ID:              "devweb-websocket",
			Area:            "WebSocket",
			Construct:       "load.WebSocket open/send/receive/close",
			Status:          "supported",
			ExecutionStatus: "executable",
			Fields:          []string{"constructor URL", "send body", "receive timeout", "close"},
			Notes:           "Converted into ws_connect/ws_send/ws_receive/ws_close actions when constructor URL can be statically resolved.",
		},
		{
			ID:              "devweb-grpc-unary",
			Area:            "gRPC",
			Construct:       "client.invoke(service, method, payload)",
			Status:          "partial",
			ExecutionStatus: "review_required",
			ReviewRequired:  true,
			Fields:          []string{"service", "method", "payload"},
			Notes:           "Converted into grpc_unary actions. Proto files, reflection settings, metadata, and payload schemas require review before promotion.",
		},
		{
			ID:              "devweb-action-groups",
			Area:            "Transactions",
			Construct:       "load.action(name, fn)",
			Status:          "supported",
			ExecutionStatus: "executable",
			Fields:          []string{"name", "body"},
			Notes:           "Action names are used as stable prefixes for generated Stormrunner action names.",
		},
		{
			ID:              "devweb-dynamic-javascript",
			Area:            "JavaScript Runtime",
			Construct:       "dynamic variables, loops, custom modules, async control flow",
			Status:          "review_only",
			ExecutionStatus: "not_executable",
			ReviewRequired:  true,
			Notes:           "The import path is static-analysis based; complex JavaScript is preserved as review evidence but not executed directly.",
		},
		{
			ID:              "devweb-correlation",
			Area:            "Correlation",
			Construct:       "custom extraction/correlation helpers",
			Status:          "partial",
			ExecutionStatus: "review_required",
			ReviewRequired:  true,
			Notes:           "Generated scripts should use Stormrunner correlation preview/apply flows to verify extracted values before approval.",
		},
	}
}

func GetSupportMatrixNotes() []string {
	return []string{
		"DevWeb import is intended as a review-and-promotion workflow, not a JavaScript runtime compatibility layer.",
		"Mixed HTTP/WebSocket/gRPC DevWeb files are converted into one review script and should be split by protocol before high-scale execution.",
		"gRPC imports require proto/reflection and metadata review because DevWeb payloads do not always carry full schema context.",
	}
}

func GetSupportMatrixExamples() []TestedExample {
	return []TestedExample{
		{
			Title:    "HTTP WebRequest",
			Coverage: "Method, URL, headers, body, and think time conversion.",
			Test:     "pkg/convert/devweb/converter_test.go",
		},
		{
			Title:    "WebSocket flow",
			Coverage: "Connect, send, receive, close, and dominant protocol detection.",
			Test:     "pkg/api/handlers_scripts_test.go",
		},
		{
			Title:    "gRPC unary",
			Coverage: "Unary invoke conversion with review-required runtime metadata.",
			Test:     "pkg/convert/devweb/converter_test.go",
		},
	}
}
