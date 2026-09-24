package enterprisearchive

import (
	"fmt"
	"strings"
)

// JavaImportSurfaceSupportEntry describes how one Java Vuser API family is
// handled by the enterprise archive importer.
type JavaImportSurfaceSupportEntry struct {
	ID              string   `json:"id"`
	Area            string   `json:"area"`
	Construct       string   `json:"construct"`
	Status          string   `json:"status"`
	ExecutionStatus string   `json:"execution_status"`
	ReviewRequired  bool     `json:"review_required"`
	Fields          []string `json:"fields,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

// JavaImportSurfaceCoverage returns the top-level Java Vuser surface matrix.
func JavaImportSurfaceCoverage() []JavaImportSurfaceSupportEntry {
	return []JavaImportSurfaceSupportEntry{
		{
			ID:              "java-lr-api",
			Area:            "LoadRunner Utility API",
			Construct:       "lrapi.lr.*",
			Status:          "supported",
			ExecutionStatus: "external_runner_required",
			ReviewRequired:  true,
			Fields:          []string{"transaction", "parameter", "message", "runtime", "timer"},
			Notes:           "The closed lr API is classified for review and transaction evidence; its source remains in the combined governed Java Vuser draft.",
		},
		{
			ID:              "java-vm-runtime-settings",
			Area:            "Java VM",
			Construct:       "default.cfg [JavaVM:Options]",
			Status:          "supported",
			ExecutionStatus: "external_runner_required",
			ReviewRequired:  true,
			Fields:          []string{"config.java_vuser.jdk_selection", "config.java_vuser.jdk_home", "config.java_vuser.additional_vm_parameters", "config.java_vuser.classpath", "config.java_vuser.use_xbootclasspath", "config.java_vuser.dedicated_class_loader"},
			Notes:           "JDK, exact JVM argument, precompiled classpath, Xboot compatibility, and dedicated-loader settings are imported with per-setting parity status and validated by the governed runner before execution.",
		},
		{
			ID:              "java-web-http",
			Area:            "HTTP",
			Construct:       "web.* / HttpURLConnection / HttpClient / Apache HttpClient",
			Status:          "supported",
			ExecutionStatus: "external_runner_required",
			ReviewRequired:  true,
			Fields:          []string{"url", "method", "headers", "body", "transactions"},
			Notes:           "HTTP evidence is reported for review, but Java source remains in one java_vuser draft so fields and phase ordering are preserved.",
		},
		{
			ID:              "java-jms",
			Area:            "Messaging",
			Construct:       "javax.jms / jakarta.jms",
			Status:          "supported",
			ExecutionStatus: "external_runner_required",
			ReviewRequired:  true,
			Fields:          []string{"provider", "destination", "body", "properties", "transactions"},
			Notes:           "JMS evidence is reported for review; calls remain in the same source-order-preserving Java Vuser draft as every other Java protocol family.",
		},
		{
			ID:              "java-jdbc",
			Area:            "Database",
			Construct:       "java.sql DriverManager/Connection/PreparedStatement",
			Status:          "supported",
			ExecutionStatus: "external_runner_required",
			ReviewRequired:  true,
			Fields:          []string{"sql", "query", "exec", "commit", "rollback"},
			Notes:           "JDBC evidence is reported for review; statements are not extracted into a separate database script.",
		},
		{
			ID:              "java-soap",
			Area:            "SOAP",
			Construct:       "JAX-WS / SOAPMessage / Axis / CXF",
			Status:          "supported",
			ExecutionStatus: "external_runner_required",
			ReviewRequired:  true,
			Fields:          []string{"endpoint", "envelope"},
			Notes:           "SOAP evidence is reported for review; clients remain part of the combined Java Vuser runner draft.",
		},
		{
			ID:              "java-no-runtime-surfaces",
			Area:            "Review",
			Construct:       "RMI, EJB, CORBA, SAP GUI Java, Jacada, Oracle NCA, Tuxedo",
			Status:          "supported",
			ExecutionStatus: "external_runner_required",
			ReviewRequired:  true,
			Notes:           "Middleware calls are preserved in the combined source and require the governed runner classpath to supply their dependencies.",
		},
		{
			ID:              "java-custom-code",
			Area:            "Custom Java",
			Construct:       "helpers, third-party clients, arbitrary Java",
			Status:          "supported",
			ExecutionStatus: "external_runner_required",
			ReviewRequired:  true,
			Notes:           "Arbitrary Java is preserved in one review draft; launch requires authoring approval and an enabled governed runner.",
		},
	}
}

// JavaHTTPIdiomCoverage returns HTTP idioms recognized as review evidence in a
// combined Java Vuser draft.
func JavaHTTPIdiomCoverage() []JavaImportSurfaceSupportEntry {
	return []JavaImportSurfaceSupportEntry{
		{ID: "java-web-url", Area: "HTTP", Construct: "web.url", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"name", "URL"}, Notes: "Preserved in the combined Java source; no native HTTP lane is emitted."},
		{ID: "java-web-submit-data", Area: "HTTP", Construct: "web.submitData", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"name", "Action", "Method", "ItemData"}, Notes: "Preserved in the combined Java source; no native HTTP lane is emitted."},
		{ID: "java-web-custom-request", Area: "HTTP", Construct: "web.customRequest", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"name", "URL", "Method", "Body"}, Notes: "Preserved in the combined Java source; no native HTTP lane is emitted."},
		{ID: "java-web-rest-request", Area: "HTTP", Construct: "web.restRequest", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"name", "URL", "Method", "Body"}, Notes: "Preserved in the combined Java source; no native HTTP lane is emitted."},
		{ID: "java-http-client-static-url", Area: "HTTP", Construct: "HttpURLConnection / Java HttpClient / Apache HttpClient with static URL", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"url", "method", "headers", "body"}, Notes: "Client evidence is classified for review while the original calls remain in the combined runner draft."},
	}
}

// JavaJMSIdiomCoverage returns JMS idioms recognized as review evidence in a
// combined Java Vuser draft.
func JavaJMSIdiomCoverage() []JavaImportSurfaceSupportEntry {
	return []JavaImportSurfaceSupportEntry{
		{ID: "java-jms-config", Area: "JMS", Construct: "JNDI env.put / InitialContext", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"broker_url", "provider", "vpn", "credentials"}, Notes: "JNDI setup remains in init on the same persistent per-VUser Actions instance used by action and end."},
		{ID: "java-jms-destination", Area: "JMS", Construct: "createQueue / createTopic / lookup", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"destination", "destination_type"}, Notes: "Destination setup remains on the same persistent per-VUser Actions instance across init, action iterations, and end."},
		{ID: "java-jms-message", Area: "JMS", Construct: "createTextMessage / readFile / replaceAll(lr.eval_string)", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"body", "template"}, Notes: "Message construction remains in the combined draft and can use Java fields and saved LoadRunner parameters retained by the persistent per-VUser session."},
		{ID: "java-jms-publish-consume", Area: "JMS", Construct: "producer.send / consumer.receive", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"publish", "consume"}, Notes: "Calls execute through the governed Java runner, not native JMS actions."},
		{ID: "java-jms-transaction", Area: "JMS", Construct: "commit / rollback / acknowledge", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"commit", "rollback", "acknowledge"}, Notes: "Session ownership and ordering remain in source and persist on the same per-VUser Actions instance across lifecycle calls."},
	}
}

// JavaJDBCIdiomCoverage returns JDBC idioms recognized as review evidence in a
// combined Java Vuser draft.
func JavaJDBCIdiomCoverage() []JavaImportSurfaceSupportEntry {
	return []JavaImportSurfaceSupportEntry{
		{ID: "java-jdbc-connect", Area: "JDBC", Construct: "DriverManager.getConnection", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"dsn"}, Notes: "Connection-setup source remains in the governed Java draft; runtime credentials stay operator-provisioned and connection state persists on the same per-VUser Actions instance."},
		{ID: "java-jdbc-query", Area: "JDBC", Construct: "PreparedStatement.executeQuery", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"select_sql"}, Notes: "Queries remain in the combined Java source; no db_query lane is emitted."},
		{ID: "java-jdbc-exec", Area: "JDBC", Construct: "executeUpdate / execute", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"dml_sql"}, Notes: "Statements remain in the combined Java source; no db_exec lane is emitted."},
		{ID: "java-jdbc-transactions", Area: "JDBC", Construct: "setAutoCommit(false) / commit / rollback", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Fields: []string{"begin", "commit", "rollback"}, Notes: "Connection and transaction ownership remain in source and persist on the same per-VUser Actions instance across lifecycle calls."},
		{ID: "java-jdbc-dynamic-sql", Area: "JDBC", Construct: "runtime-built SQL", Status: "supported", ExecutionStatus: "external_runner_required", ReviewRequired: true, Notes: "Dynamic SQL remains intact in the governed runner draft."},
	}
}

// RenderJavaImportSurfaceCoverageMarkdown renders the generated Java Vuser
// first-class importer disposition matrix.
func RenderJavaImportSurfaceCoverageMarkdown() string {
	var b strings.Builder
	b.WriteString("## Java Vuser import coverage\n\n")
	b.WriteString("This matrix covers the Java Vuser importer surfaces outside the closed `lrapi.lr` utility API. ")
	b.WriteString("Every Java archive remains one `java_vuser` draft so init/action/end source ordering and per-VUser Java state are preserved. ")
	b.WriteString("Static idiom analysis supplies review evidence only; launch requires approval plus an enabled governed JVM runner advertising `java_vuser_persistent_lifecycle_v1` and `java_vuser_session_frame_token_v1`.\n\n")

	writeJavaSurfaceCoverageTable(&b, "Surface Disposition", JavaImportSurfaceCoverage())
	writeJavaSurfaceCoverageTable(&b, "HTTP Idioms", JavaHTTPIdiomCoverage())
	writeJavaSurfaceCoverageTable(&b, "JMS Idioms", JavaJMSIdiomCoverage())
	writeJavaSurfaceCoverageTable(&b, "JDBC Idioms", JavaJDBCIdiomCoverage())

	b.WriteString("The same rows are published by `GET /api/v1/scripts/import/enterprise-script-archive/support-matrix` for dashboard and API clients.\n")
	return b.String()
}

func writeJavaSurfaceCoverageTable(b *strings.Builder, title string, rows []JavaImportSurfaceSupportEntry) {
	fmt.Fprintf(b, "### %s\n\n", title)
	b.WriteString("| ID | Area | Construct | Status | Execution | Review | Fields | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, row := range rows {
		review := "no"
		if row.ReviewRequired {
			review = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | `%s` | `%s` | %s | %s | %s |\n",
			row.ID,
			escapeMarkdownTableCell(row.Area),
			escapeMarkdownTableCell(row.Construct),
			row.Status,
			row.ExecutionStatus,
			review,
			escapeMarkdownTableCell(strings.Join(row.Fields, ", ")),
			escapeMarkdownTableCell(row.Notes),
		)
	}
	b.WriteString("\n")
}

func escapeMarkdownTableCell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	return value
}
