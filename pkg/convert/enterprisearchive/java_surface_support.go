package enterprisearchive

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
			ExecutionStatus: "classified",
			Fields:          []string{"transaction", "parameter", "message", "runtime", "timer"},
			Notes:           "The closed lr API is fully classified by java_lr_function_matrix; executable effects map to native actions or config, non-executable effects are named review dispositions.",
		},
		{
			ID:              "java-web-http",
			Area:            "HTTP",
			Construct:       "web.* / HttpURLConnection / HttpClient / Apache HttpClient",
			Status:          "supported",
			ExecutionStatus: "executable",
			Fields:          []string{"url", "method", "headers", "body", "transactions"},
			Notes:           "Recognized Java-over-HTTP idioms are converted through the same executable HTTP path used by C web_* imports; generic Java object-over-HTTP remains review-only.",
		},
		{
			ID:              "java-jms",
			Area:            "Messaging",
			Construct:       "javax.jms / jakarta.jms",
			Status:          "supported",
			ExecutionStatus: "executable",
			Fields:          []string{"provider", "destination", "body", "properties", "transactions"},
			Notes:           "JMS provider and queue/topic targeting are inferred from source evidence, with broker credentials redacted into secret references.",
		},
		{
			ID:              "java-jdbc",
			Area:            "Database",
			Construct:       "java.sql DriverManager/Connection/PreparedStatement",
			Status:          "supported",
			ExecutionStatus: "executable",
			Fields:          []string{"sql", "query", "exec", "commit", "rollback"},
			Notes:           "Statically recoverable JDBC statements are delegated to the database importer and emitted as db_query/db_exec/transaction actions.",
		},
		{
			ID:              "java-soap",
			Area:            "SOAP",
			Construct:       "JAX-WS / SOAPMessage / Axis / CXF",
			Status:          "partial",
			ExecutionStatus: "scoped_executable",
			ReviewRequired:  true,
			Fields:          []string{"endpoint", "envelope"},
			Notes:           "Recoverable static endpoint/envelope calls become native HTTP SOAP requests; dynamic SOAP clients remain named review items.",
		},
		{
			ID:              "java-no-runtime-surfaces",
			Area:            "Review",
			Construct:       "RMI, EJB, CORBA, SAP GUI Java, Jacada, Oracle NCA, Tuxedo",
			Status:          "review_only",
			ExecutionStatus: "not_executable",
			ReviewRequired:  true,
			Notes:           "No-runtime Java middleware surfaces receive deterministic named review dispositions instead of generic fallthrough.",
		},
		{
			ID:              "java-custom-code",
			Area:            "Custom Java",
			Construct:       "helpers, third-party clients, arbitrary Java",
			Status:          "review_only",
			ExecutionStatus: "external_runner_opt_in",
			ReviewRequired:  true,
			Notes:           "Arbitrary Java is preserved for review by default; governed java_vuser sidecar replay requires explicit operator opt-in.",
		},
	}
}

// JavaHTTPIdiomCoverage returns the Java-over-HTTP idioms recognized as native
// HTTP actions.
func JavaHTTPIdiomCoverage() []JavaImportSurfaceSupportEntry {
	return []JavaImportSurfaceSupportEntry{
		{ID: "java-web-url", Area: "HTTP", Construct: "web.url", Status: "supported", ExecutionStatus: "executable", Fields: []string{"name", "URL"}, Notes: "Delegates to web_url conversion and emits http_request."},
		{ID: "java-web-submit-data", Area: "HTTP", Construct: "web.submitData", Status: "supported", ExecutionStatus: "executable", Fields: []string{"name", "Action", "Method", "ItemData"}, Notes: "Delegates to web_submit_data conversion."},
		{ID: "java-web-custom-request", Area: "HTTP", Construct: "web.customRequest", Status: "supported", ExecutionStatus: "executable", Fields: []string{"name", "URL", "Method", "Body"}, Notes: "Delegates to web_custom_request conversion and preserves ordered header_list."},
		{ID: "java-web-rest-request", Area: "HTTP", Construct: "web.restRequest", Status: "supported", ExecutionStatus: "executable", Fields: []string{"name", "URL", "Method", "Body"}, Notes: "Delegates to web_rest conversion."},
		{ID: "java-http-client-static-url", Area: "HTTP", Construct: "HttpURLConnection / Java HttpClient / Apache HttpClient with static URL", Status: "partial", ExecutionStatus: "executable", ReviewRequired: true, Fields: []string{"url", "method"}, Notes: "Simple static URL sends are promoted; dynamic builders remain named review items."},
	}
}

// JavaJMSIdiomCoverage returns the JMS idioms recognized as native JMS actions.
func JavaJMSIdiomCoverage() []JavaImportSurfaceSupportEntry {
	return []JavaImportSurfaceSupportEntry{
		{ID: "java-jms-config", Area: "JMS", Construct: "JNDI env.put / InitialContext", Status: "supported", ExecutionStatus: "executable", Fields: []string{"broker_url", "provider", "vpn", "credentials"}, Notes: "JNDI evidence configures the native JMS provider with credentials redacted."},
		{ID: "java-jms-destination", Area: "JMS", Construct: "createQueue / createTopic / lookup", Status: "supported", ExecutionStatus: "executable", Fields: []string{"destination", "destination_type"}, Notes: "Queue/topic targeting is inferred from source evidence."},
		{ID: "java-jms-message", Area: "JMS", Construct: "createTextMessage / readFile / replaceAll(lr.eval_string)", Status: "supported", ExecutionStatus: "executable", Fields: []string{"body", "template"}, Notes: "XML/text bodies are recovered and parameterized where statically visible."},
		{ID: "java-jms-publish-consume", Area: "JMS", Construct: "producer.send / consumer.receive", Status: "supported", ExecutionStatus: "executable", Fields: []string{"jms_publish", "jms_consume"}, Notes: "Mapped to native JMS publish and consume actions."},
		{ID: "java-jms-transaction", Area: "JMS", Construct: "commit / rollback / acknowledge", Status: "supported", ExecutionStatus: "executable", Fields: []string{"jms_commit", "jms_rollback", "jms_ack"}, Notes: "Session transaction and acknowledgement calls are converted to native actions."},
	}
}

// JavaJDBCIdiomCoverage returns the JDBC idioms recognized as native database
// actions.
func JavaJDBCIdiomCoverage() []JavaImportSurfaceSupportEntry {
	return []JavaImportSurfaceSupportEntry{
		{ID: "java-jdbc-connect", Area: "JDBC", Construct: "DriverManager.getConnection", Status: "partial", ExecutionStatus: "review_required", ReviewRequired: true, Fields: []string{"dsn"}, Notes: "Connection literals are redacted to {{secret.database_dsn}}; operators must bind the real DSN."},
		{ID: "java-jdbc-query", Area: "JDBC", Construct: "PreparedStatement.executeQuery", Status: "supported", ExecutionStatus: "executable", Fields: []string{"select_sql"}, Notes: "Static SELECT SQL becomes db_query through the database importer."},
		{ID: "java-jdbc-exec", Area: "JDBC", Construct: "executeUpdate / execute", Status: "supported", ExecutionStatus: "executable", Fields: []string{"dml_sql"}, Notes: "Static DML/DDL SQL becomes db_exec through the database importer."},
		{ID: "java-jdbc-transactions", Area: "JDBC", Construct: "setAutoCommit(false) / commit / rollback", Status: "supported", ExecutionStatus: "executable", Fields: []string{"db_begin", "db_commit", "db_rollback"}, Notes: "Transaction controls map to native database transaction actions."},
		{ID: "java-jdbc-dynamic-sql", Area: "JDBC", Construct: "runtime-built SQL", Status: "review_only", ExecutionStatus: "not_executable", ReviewRequired: true, Notes: "Unrecoverable dynamic SQL is preserved for source review instead of fabricated."},
	}
}
