package enterprisearchive

import (
	"strings"
	"testing"
)

func TestJavaHTTPClassifierUsesReceiverTypeContext(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "jdk_builder_send",
			source: `HttpClient client = HttpClient.newBuilder().build();
client.send(request, HttpResponse.BodyHandlers.ofString());`,
			want: true,
		},
		{
			name: "jdk_builder_send_async",
			source: `var builder = HttpClient.newBuilder();
var client = builder.build();
client.sendAsync(request, HttpResponse.BodyHandlers.ofString());`,
			want: true,
		},
		{
			name: "typed_injected_jdk_client",
			source: `java.net.http.HttpClient client = injected;
client.send(request, handler);`,
			want: true,
		},
		{
			name: "typed_apache_client",
			source: `CloseableHttpClient client = injected;
client.execute(request);`,
			want: true,
		},
		{
			name: "chained_jdk_builder",
			source: `HttpClient.newBuilder().build()
    .send(request, HttpResponse.BodyHandlers.ofString());`,
			want: true,
		},
		{
			name: "direct_jdk_client",
			source: `HttpClient.newHttpClient()
    .send(request, HttpResponse.BodyHandlers.ofString());`,
			want: true,
		},
		{
			name: "configured_jdk_builder",
			source: `var client = HttpClient.newBuilder()
    .connectTimeout(timeout)
    .followRedirects(HttpClient.Redirect.NORMAL)
    .build();
client.send(request, handler);`,
			want: true,
		},
		{
			name: "configured_apache_builder",
			source: `var client = HttpClients.custom()
    .disableCookieManagement()
    .build();
client.execute(request);`,
			want: true,
		},
		{
			name: "generic_send_with_unused_http_builder",
			source: `var unusedClient = HttpClient.newBuilder().build();
messageBus.send(payload);`,
			want: false,
		},
		{
			name: "generic_send_with_unused_http_client_factory",
			source: `var unusedClient = HttpClient.newHttpClient();
messageBus.send(payload);`,
			want: false,
		},
		{
			name: "generic_execute_with_unused_http_type",
			source: `HttpClient unusedClient;
worker.execute(task);`,
			want: false,
		},
		{
			name: "untyped_member_does_not_borrow_local_type",
			source: `HttpClient client = injected;
holder.client.send(payload);`,
			want: false,
		},
		{
			name: "comment_literal_and_import_only",
			source: `import java.net.http.HttpClient;
String fake = "HttpClient client = null; client.send(request, handler);";
// HttpClient.newBuilder().build().send(request, handler);
Object worker = null;
worker.send(payload);`,
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := javaLooksLikeHTTP(tc.source); got != tc.want {
				t.Fatalf("javaLooksLikeHTTP() = %v, want %v for source:\n%s", got, tc.want, tc.source)
			}
		})
	}
}

func TestJavaJDBCClassifierUsesReceiverTypeContext(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "connection_transaction",
			source: `Connection connection = suppliedConnection;
connection.commit();`,
			want: true,
		},
		{
			name: "statement_execute",
			source: `Statement statement = suppliedStatement;
statement.execute(sql);`,
			want: true,
		},
		{
			name: "prepared_statement_query",
			source: `PreparedStatement statement = suppliedStatement;
statement.executeQuery();`,
			want: true,
		},
		{
			name: "qualified_prepared_statement_update",
			source: `java.sql.PreparedStatement statement = suppliedStatement;
statement.executeUpdate();`,
			want: true,
		},
		{
			name: "var_statement_factory",
			source: `Connection connection = suppliedConnection;
var statement = connection.createStatement();
statement.execute(sql);`,
			want: true,
		},
		{
			name: "generic_create_statement_chain",
			source: `var statement = document.createStatement();
statement.execute(sql);`,
			want: false,
		},
		{
			name: "generic_execute",
			source: `Executor worker = suppliedExecutor;
worker.execute(task);`,
			want: false,
		},
		{
			name: "generic_execute_with_unused_statement",
			source: `Statement unusedStatement = null;
worker.execute(task);`,
			want: false,
		},
		{
			name: "generic_commit_with_unused_qualified_statement",
			source: `java.sql.Statement unusedStatement = null;
unitOfWork.commit();`,
			want: false,
		},
		{
			name: "comment_literal_and_unused_import",
			source: `import java.sql.*;
String fake = "Statement statement = null; statement.execute(sql);";
// PreparedStatement ps = null; ps.executeQuery();
Object worker = null;
worker.execute(task);`,
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := javaLooksLikeDatabase(tc.source); got != tc.want {
				t.Fatalf("javaLooksLikeDatabase() = %v, want %v for source:\n%s", got, tc.want, tc.source)
			}
		})
	}
}

func TestJavaClassifierHonorsLifecycleReceiverShadowing(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		surface javaSurface
		want    bool
	}{
		{
			name: "http_local_shadows_field",
			source: `public class Actions {
  private HttpClient client;
  public int action() {
    CustomClient client = suppliedClient;
    client.send(payload);
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
			want:    false,
		},
		{
			name: "explicit_http_field_ignores_local_shadow",
			source: `public class Actions {
  private HttpClient client;
  public int action() throws Exception {
    CustomClient client = suppliedClient;
    this.client.send(request, handler);
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
			want:    true,
		},
		{
			name: "jdbc_local_shadows_field",
			source: `public class Actions {
  private Statement statement;
  public int action() {
    TaskRunner statement = suppliedRunner;
    statement.execute(task);
    return 0;
  }
}`,
			surface: javaSurfaceDatabase,
			want:    false,
		},
		{
			name: "explicit_jdbc_field_ignores_local_shadow",
			source: `public class Actions {
  private Statement statement;
  public int action() throws Exception {
    TaskRunner statement = suppliedRunner;
    this.statement.execute(sql);
    return 0;
  }
}`,
			surface: javaSurfaceDatabase,
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": javaClassifierSourceWithProtocolImport(tc.source, tc.surface),
			}
			program, warnings := parseJavaProgram(files)
			if len(warnings) != 0 {
				t.Fatalf("parse warnings: %v", warnings)
			}
			profile := classifyJavaArchive(program, nil, files)
			if got := profile.Surfaces[tc.surface]; got != tc.want {
				t.Fatalf("surface %q = %v, want %v; evidence:\n%s", tc.surface, got, tc.want, javaExecutableEvidenceText(program))
			}
		})
	}
}

func TestJavaClassifierKeepsHelperAndBlockReceiverScopes(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		surface javaSurface
		want    bool
	}{
		{
			name: "helper_local_does_not_type_caller_receiver",
			source: `public class Actions {
  private CustomClient client;
  private void prepareHttp() {
    HttpClient client = injectedHttpClient;
  }
  public int action() {
    prepareHttp();
    client.send(payload);
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
			want:    false,
		},
		{
			name: "helper_local_does_not_clear_caller_field",
			source: `public class Actions {
  private HttpClient client;
  private void prepareCustom() {
    CustomClient client = suppliedClient;
    client.prepare();
  }
  public int action() throws Exception {
    prepareCustom();
    client.send(request, handler);
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
			want:    true,
		},
		{
			name: "nested_local_does_not_type_outer_receiver",
			source: `public class Actions {
  private CustomClient client;
  public int action() {
    if (enabled) {
      HttpClient client = injectedHttpClient;
    }
    client.send(payload);
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
			want:    false,
		},
		{
			name: "nested_local_does_not_clear_outer_field",
			source: `public class Actions {
  private HttpClient client;
  public int action() throws Exception {
    if (enabled) {
      CustomClient client = suppliedClient;
      client.prepare();
    }
    client.send(request, handler);
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
			want:    true,
		},
		{
			name: "reachable_helper_parameter_is_typed",
			source: `public class Actions {
  private HttpClient injectedHttpClient;
  private void issueRequest(HttpClient client) throws Exception {
    client.send(request, handler);
  }
  public int action() throws Exception {
    issueRequest(injectedHttpClient);
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
			want:    true,
		},
		{
			name: "unreachable_helper_is_not_evidence",
			source: `public class Actions {
  private void unused() throws Exception {
    HttpClient client = injectedHttpClient;
    client.send(request, handler);
  }
  public int action() {
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
			want:    false,
		},
		{
			name: "jdbc_helper_local_does_not_type_caller_receiver",
			source: `public class Actions {
  private TaskRunner statement;
  private void prepareStatement() {
    Statement statement = suppliedStatement;
  }
  public int action() {
    prepareStatement();
    statement.execute(task);
    return 0;
  }
}`,
			surface: javaSurfaceDatabase,
			want:    false,
		},
		{
			name: "jdbc_nested_local_does_not_clear_outer_field",
			source: `public class Actions {
  private Statement statement;
  public int action() throws Exception {
    if (enabled) {
      TaskRunner statement = suppliedRunner;
      statement.prepare();
    }
    statement.execute(sql);
    return 0;
  }
}`,
			surface: javaSurfaceDatabase,
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": javaClassifierSourceWithProtocolImport(tc.source, tc.surface),
			}
			program, warnings := parseJavaProgram(files)
			if len(warnings) != 0 {
				t.Fatalf("parse warnings: %v", warnings)
			}
			profile := classifyJavaArchive(program, nil, files)
			if got := profile.Surfaces[tc.surface]; got != tc.want {
				t.Fatalf("surface %q = %v, want %v; evidence:\n%s", tc.surface, got, tc.want, javaExecutableEvidenceText(program))
			}
		})
	}
}

func TestJavaClassifierReportsBuilderAndStatementReviewEvidence(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		surface       javaSurface
		signalSnippet string
	}{
		{
			name: "http",
			source: `public class Actions {
  public int action() throws Exception {
    HttpClient client = HttpClient.newBuilder().build();
    client.send(request, handler);
    return 0;
  }
}`,
			surface:       javaSurfaceHTTP,
			signalSnippet: "java http api usage",
		},
		{
			name: "jdbc",
			source: `public class Actions {
  public int action() throws Exception {
    Statement statement = suppliedStatement;
    statement.execute(sql);
    return 0;
  }
}`,
			surface:       javaSurfaceDatabase,
			signalSnippet: "java.sql api usage",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": javaClassifierSourceWithProtocolImport(tc.source, tc.surface),
			}
			program, warnings := parseJavaProgram(files)
			if len(warnings) != 0 {
				t.Fatalf("parse warnings: %v", warnings)
			}
			profile := classifyJavaArchive(program, nil, files)
			if !profile.Surfaces[tc.surface] {
				t.Fatalf("profile surfaces = %#v, want %q review evidence", profile.Surfaces, tc.surface)
			}

			detected := DetectProtocol(files, &ProjectConfig{ScriptType: "Java Vuser"}, nil)
			if !strings.Contains(strings.ToLower(strings.Join(detected.Signals, "\n")), tc.signalSnippet) {
				t.Fatalf("detected signals = %#v, want %q", detected.Signals, tc.signalSnippet)
			}
		})
	}
}

func TestJavaClassifierRejectsReceiverBlindProtocolMethodNames(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		surface javaSurface
		want    bool
	}{
		{
			name: "renderer_named_web",
			source: `public class Actions {
  private Renderer web;
  public int action() { web.render(); return 0; }
}`,
			surface: javaSurfaceHTTP,
			want:    false,
		},
		{
			name: "undeclared_web_unknown_method",
			source: `public class Actions {
  public int action() { web.render(); return 0; }
}`,
			surface: javaSurfaceHTTP,
			want:    false,
		},
		{
			name: "loadrunner_web_url",
			source: `public class Actions {
  public int action() { web.url("home", "URL=https://example.test", null); return 0; }
}`,
			surface: javaSurfaceHTTP,
			want:    true,
		},
		{
			name: "mailbox_receive",
			source: `public class Actions {
  private Mailbox mailbox;
  public int action() { mailbox.receive(); return 0; }
}`,
			surface: javaSurfaceJMS,
			want:    false,
		},
		{
			name: "jms_consumer_receive",
			source: `public class Actions {
  private MessageConsumer consumer;
  public int action() { consumer.receive(); return 0; }
}`,
			surface: javaSurfaceJMS,
			want:    true,
		},
		{
			name: "dispatcher_create_dispatch",
			source: `public class Actions {
  private Dispatcher dispatcher;
  public int action() { dispatcher.createDispatch(port, type, mode); return 0; }
}`,
			surface: javaSurfaceSOAP,
			want:    false,
		},
		{
			name: "soap_service_create_dispatch",
			source: `public class Actions {
  private Service service;
  public int action() { service.createDispatch(port, type, mode); return 0; }
}`,
			surface: javaSurfaceSOAP,
			want:    true,
		},
		{
			name: "custom_connector_open_connection",
			source: `public class Actions {
  private CustomConnector connector;
  public int action() { connector.openConnection(); return 0; }
}`,
			surface: javaSurfaceHTTP,
			want:    false,
		},
		{
			name: "java_url_open_connection",
			source: `public class Actions {
  private java.net.URL endpoint;
  public int action() throws Exception { endpoint.openConnection(); return 0; }
}`,
			surface: javaSurfaceHTTP,
			want:    true,
		},
		{
			name: "constructed_java_url_open_connection",
			source: `public class Actions {
  public int action() throws Exception { new java.net.URL(target).openConnection(); return 0; }
}`,
			surface: javaSurfaceHTTP,
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.source
			if tc.want {
				source = javaClassifierSourceWithProtocolImport(source, tc.surface)
			}
			profile := classifyJavaTestProfile(t, map[string]string{"Actions.java": source})
			if got := profile.Surfaces[tc.surface]; got != tc.want {
				t.Fatalf("surface %q = %v, want %v", tc.surface, got, tc.want)
			}
		})
	}
}

func TestJavaClassifierHonorsEnhancedForAndLambdaScopes(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name: "enhanced_for_shadows_http_field",
			source: `public class Actions {
  private HttpClient client;
  public int action() {
    for (CustomClient client : clients) {
      client.send(payload);
    }
    return 0;
  }
}`,
			want: false,
		},
		{
			name: "enhanced_for_shadow_ends_at_block",
			source: `public class Actions {
  private HttpClient client;
  public int action() throws Exception {
    for (CustomClient client : clients) {
      client.prepare();
    }
    client.send(request, handler);
    return 0;
  }
}`,
			want: true,
		},
		{
			name: "implicit_expression_lambda_shadows_field",
			source: `public class Actions {
  private HttpClient client;
  public int action() {
    clients.forEach(client -> client.send(payload));
    return 0;
  }
}`,
			want: false,
		},
		{
			name: "implicit_block_lambda_shadows_field",
			source: `public class Actions {
  private HttpClient client;
  public int action() {
    clients.forEach(client -> {
      client.send(payload);
    });
    return 0;
  }
}`,
			want: false,
		},
		{
			name: "lambda_shadow_ends_after_expression",
			source: `public class Actions {
  private HttpClient client;
  public int action() throws Exception {
    clients.forEach(client -> client.prepare());
    client.send(request, handler);
    return 0;
  }
}`,
			want: true,
		},
		{
			name: "typed_http_lambda_parameter",
			source: `public class Actions {
  public int action() {
    clients.forEach((HttpClient client) -> client.send(request, handler));
    return 0;
  }
}`,
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := javaClassifierSourceWithProtocolImport(tc.source, javaSurfaceHTTP)
			profile := classifyJavaTestProfile(t, map[string]string{"Actions.java": source})
			if got := profile.Surfaces[javaSurfaceHTTP]; got != tc.want {
				t.Fatalf("HTTP surface = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestJavaClassifierResolvesCustomAndStandardProtocolTypes(t *testing.T) {
	t.Run("archive_custom_simple_names", func(t *testing.T) {
		tests := []struct {
			name       string
			customType string
			fieldType  string
			call       string
			surface    javaSurface
		}{
			{"http_client", "HttpClient", "HttpClient client", "client.send(payload)", javaSurfaceHTTP},
			{"connection", "Connection", "Connection connection", "connection.commit()", javaSurfaceDatabase},
			{"statement", "Statement", "Statement statement", "statement.execute(task)", javaSurfaceDatabase},
			{"data_source", "DataSource", "DataSource source", "source.getConnection()", javaSurfaceDatabase},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				files := map[string]string{
					"Actions.java": `public class Actions {
  private ` + tc.fieldType + `;
  public int action() { ` + tc.call + `; return 0; }
}`,
					tc.customType + ".java": `public class ` + tc.customType + ` { public void noop() {} }`,
				}
				profile := classifyJavaTestProfile(t, files)
				if profile.Surfaces[tc.surface] {
					t.Fatalf("surfaces = %#v, archive custom %s must not be protocol evidence", profile.Surfaces, tc.customType)
				}
			})
		}
	})

	t.Run("standard_imports_and_qualified_types", func(t *testing.T) {
		tests := []struct {
			name    string
			source  string
			surface javaSurface
		}{
			{
				name: "imported_http_client",
				source: `import java.net.http.HttpClient;
public class Actions {
  private HttpClient client;
  public int action() throws Exception { client.send(request, handler); return 0; }
}`,
				surface: javaSurfaceHTTP,
			},
			{
				name: "qualified_http_client",
				source: `public class Actions {
  private java.net.http.HttpClient client;
  public int action() throws Exception { client.send(request, handler); return 0; }
}`,
				surface: javaSurfaceHTTP,
			},
			{
				name: "imported_jdbc_connection",
				source: `import java.sql.Connection;
public class Actions {
  private Connection connection;
  public int action() throws Exception { connection.commit(); return 0; }
}`,
				surface: javaSurfaceDatabase,
			},
			{
				name: "qualified_jdbc_statement",
				source: `public class Actions {
  private java.sql.Statement statement;
  public int action() throws Exception { statement.execute(sql); return 0; }
}`,
				surface: javaSurfaceDatabase,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				profile := classifyJavaTestProfile(t, map[string]string{"Actions.java": tc.source})
				if !profile.Surfaces[tc.surface] {
					t.Fatalf("surfaces = %#v, want %q", profile.Surfaces, tc.surface)
				}
			})
		}
	})

	t.Run("archive_custom_http_factories", func(t *testing.T) {
		tests := []struct {
			name   string
			action string
			files  map[string]string
		}{
			{
				name:   "simple_name",
				action: `HttpClient.newBuilder().build().send(request, handler);`,
				files: map[string]string{
					"HttpClient.java": `public class HttpClient { public void noop() {} }`,
				},
			},
			{
				name:   "qualified_name",
				action: `custom.HttpClient.newHttpClient().send(request, handler);`,
				files: map[string]string{
					"custom/HttpClient.java": `package custom; public class HttpClient { public void noop() {} }`,
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				files := map[string]string{
					"Actions.java": `public class Actions {
  public int action() { ` + tc.action + ` return 0; }
}`,
				}
				for name, source := range tc.files {
					files[name] = source
				}
				profile := classifyJavaTestProfile(t, files)
				if profile.Surfaces[javaSurfaceHTTP] {
					t.Fatalf("surfaces = %#v, archive custom HTTP factory must not be HTTP evidence", profile.Surfaces)
				}
			})
		}
	})

	t.Run("jms_connection_import_is_not_jdbc", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `import javax.jms.Connection;
public class Actions {
  private Connection connection;
  public int action() throws Exception { connection.createSession(false, 1); return 0; }
}`,
		}
		profile := classifyJavaTestProfile(t, files)
		if !profile.Surfaces[javaSurfaceJMS] || profile.Surfaces[javaSurfaceDatabase] {
			t.Fatalf("surfaces = %#v, want JMS without database", profile.Surfaces)
		}
	})
}

func TestJavaClassifierIncludesExecutedInitializationPaths(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		surface javaSurface
	}{
		{
			name: "field_initializer_helper",
			source: `public class Actions {
  private final Object configured = configure();
  private Object configure() throws Exception {
    HttpClient client = injectedClient;
    client.send(request, handler);
    return client;
  }
  public int action() { return 0; }
}`,
			surface: javaSurfaceHTTP,
		},
		{
			name: "instance_initializer_helper",
			source: `public class Actions {
  {
    initializeDatabase();
  }
  private void initializeDatabase() throws Exception {
    Statement statement = suppliedStatement;
    statement.execute(sql);
  }
  public int action() { return 0; }
}`,
			surface: javaSurfaceDatabase,
		},
		{
			name: "static_initializer_direct",
			source: `public class Actions {
  static {
    MessageConsumer consumer = suppliedConsumer;
    consumer.receive();
  }
  public int action() { return 0; }
}`,
			surface: javaSurfaceJMS,
		},
		{
			name: "delegated_public_constructor",
			source: `public class Actions {
  public Actions() {
    this(true);
  }
  private Actions(boolean enabled) throws Exception {
    HttpClient client = injectedClient;
    client.send(request, handler);
  }
  public int action() { return 0; }
}`,
			surface: javaSurfaceHTTP,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := javaClassifierSourceWithProtocolImport(tc.source, tc.surface)
			profile := classifyJavaTestProfile(t, map[string]string{"Actions.java": source})
			if !profile.Surfaces[tc.surface] {
				t.Fatalf("surfaces = %#v, want %q initialization evidence", profile.Surfaces, tc.surface)
			}
		})
	}
}

func TestJavaClassifierExcludesDeferredFieldBodies(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "lambda_body",
			source: `public class Actions {
  private final Runnable deferred = () -> {
    prepare();
    issueRequest();
  };
  private void issueRequest() throws Exception {
    HttpClient client = injectedClient;
    client.send(request, handler);
  }
  public int action() { return 0; }
}`,
		},
		{
			name: "anonymous_class_method",
			source: `public class Actions {
  private final Callback deferred = new Callback() {
    public void run() {
      issueRequest();
    }
  };
  private void issueRequest() throws Exception {
    HttpClient client = injectedClient;
    client.send(request, handler);
  }
  public int action() { return 0; }
}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := javaClassifierSourceWithProtocolImport(tc.source, javaSurfaceHTTP)
			profile := classifyJavaTestProfile(t, map[string]string{"Actions.java": source})
			if profile.Surfaces[javaSurfaceHTTP] {
				t.Fatalf("surfaces = %#v, deferred field body must not be executable evidence", profile.Surfaces)
			}
		})
	}
}

func TestJavaClassifierResolvesSameSimpleHelperByPackageAndImport(t *testing.T) {
	helpers := map[string]string{
		"live/Helper.java": `package live;
import java.net.http.HttpClient;
public class Helper {
  public void run() throws Exception {
    HttpClient client = injectedClient;
    client.send(request, handler);
  }
}`,
		"backup/Helper.java": `package backup;
public class Helper {
  public void run() {
    TaskRunner client = suppliedRunner;
    client.send(task);
  }
}`,
	}
	for _, tc := range []struct {
		name       string
		importName string
		wantHTTP   bool
	}{
		{"live_helper", "live.Helper", true},
		{"backup_helper", "backup.Helper", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import ` + tc.importName + `;
public class Actions {
  public int action() throws Exception {
    new Helper().run();
    return 0;
  }
}`,
			}
			for name, source := range helpers {
				files[name] = source
			}
			profile := classifyJavaTestProfile(t, files)
			if got := profile.Surfaces[javaSurfaceHTTP]; got != tc.wantHTTP {
				t.Fatalf("HTTP surface = %v, want %v; evidence:\n%s", got, tc.wantHTTP, javaExecutableEvidenceText(mustParseJavaTestProgram(t, files)))
			}
		})
	}
}

func TestJavaClassifierCountsDataSourceAndDriverManagerCalls(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "data_source_get_connection",
			source: `import javax.sql.DataSource;
public class Actions {
  private DataSource source;
  public int action() throws Exception { source.getConnection(); return 0; }
}`,
		},
		{
			name: "data_source_chained_close",
			source: `public class Actions {
  private javax.sql.DataSource source;
  public int action() throws Exception { source.getConnection().close(); return 0; }
}`,
		},
		{
			name: "imported_driver_manager",
			source: `import java.sql.DriverManager;
public class Actions {
  public int action() throws Exception { DriverManager.getConnection(url); return 0; }
}`,
		},
		{
			name: "qualified_driver_manager",
			source: `public class Actions {
  public int action() throws Exception { java.sql.DriverManager.getConnection(url); return 0; }
}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile := classifyJavaTestProfile(t, map[string]string{"Actions.java": tc.source})
			if !profile.Surfaces[javaSurfaceDatabase] {
				t.Fatalf("surfaces = %#v, want database", profile.Surfaces)
			}
		})
	}

	t.Run("archive_custom_driver_manager", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `public class Actions {
  public int action() throws Exception { DriverManager.getConnection(url); return 0; }
}`,
			"DriverManager.java": `public class DriverManager {
  public static Object getConnection(Object value) { return value; }
}`,
		}
		profile := classifyJavaTestProfile(t, files)
		if profile.Surfaces[javaSurfaceDatabase] {
			t.Fatalf("surfaces = %#v, custom DriverManager must not be database evidence", profile.Surfaces)
		}
	})
}

func classifyJavaTestProfile(t *testing.T, files map[string]string) JavaProfile {
	t.Helper()
	program := mustParseJavaTestProgram(t, files)
	return classifyJavaArchive(program, nil, files)
}

func javaClassifierSourceWithProtocolImport(source string, surface javaSurface) string {
	switch surface {
	case javaSurfaceHTTP:
		return "import java.net.http.HttpClient;\n" + source
	case javaSurfaceDatabase:
		return "import java.sql.Statement;\n" + source
	case javaSurfaceJMS:
		return "import javax.jms.MessageConsumer;\n" + source
	case javaSurfaceSOAP:
		return "import javax.xml.ws.Service;\n" + source
	default:
		return source
	}
}

func mustParseJavaTestProgram(t *testing.T, files map[string]string) javaProgram {
	t.Helper()
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings: %v", warnings)
	}
	return program
}
