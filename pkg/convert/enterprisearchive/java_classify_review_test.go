package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"
)

func TestJavaClassifierRecognizesAdditionalExecutedProtocolMethods(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		surface javaSurface
	}{
		{
			name: "loadrunner_camel_case_web",
			source: `public class Actions {
  public int action() {
    web.customRequest("request", "URL=https://example.test", null);
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
		},
		{
			name: "url_open_connection",
			source: `import java.net.URL;
public class Actions {
  private URL target;
  public int action() throws Exception {
    target.openConnection();
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
		},
		{
			name: "constructed_url_open_stream",
			source: `public class Actions {
  public int action() throws Exception {
    new java.net.URL(target).openStream();
    return 0;
  }
}`,
			surface: javaSurfaceHTTP,
		},
		{
			name: "jms_factory_create_connection",
			source: `import javax.jms.ConnectionFactory;
public class Actions {
  private ConnectionFactory factory;
  public int action() {
    factory.createConnection();
    return 0;
  }
}`,
			surface: javaSurfaceJMS,
		},
		{
			name: "soap_dispatch_invoke",
			source: `import javax.xml.ws.Dispatch;
public class Actions {
  private Dispatch dispatch;
  public int action() {
    dispatch.invoke(payload);
    return 0;
  }
}`,
			surface: javaSurfaceSOAP,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"Actions.java": tc.source}
			profile := classifyJavaArchive(mustParseJavaTestProgram(t, files), nil, files)
			if !profile.Surfaces[tc.surface] {
				t.Fatalf("surfaces = %#v, want %q", profile.Surfaces, tc.surface)
			}
		})
	}
}

func TestJavaClassifierTypesDeclarationBeforeStoredExpressionLambda(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import java.net.http.HttpClient;
public class Actions {
  private HttpClient configure(Object callback) { return null; }
  public int action() throws Exception {
    HttpClient client = configure(value -> value);
    client.send(request, handler);
    return 0;
  }
}`,
	}
	program := mustParseJavaTestProgram(t, files)
	profile := classifyJavaArchive(program, nil, files)
	if !profile.Surfaces[javaSurfaceHTTP] {
		t.Fatalf("surfaces = %#v, want HTTP; evidence:\n%s", profile.Surfaces, javaExecutableEvidenceText(program))
	}
}

func TestJavaClassifierDefersStoredCallbackBodies(t *testing.T) {
	tests := []struct {
		name        string
		declaration string
		wantHTTP    bool
	}{
		{
			name: "stored_block_lambda",
			declaration: `Runnable callback = () -> {
      issueRequest();
    };`,
		},
		{
			name:        "stored_expression_lambda",
			declaration: `Runnable callback = () -> issueRequest();`,
		},
		{
			name: "stored_anonymous_method",
			declaration: `Runnable callback = new Runnable() {
      public void run() {
        issueRequest();
      }
    };`,
		},
		{
			name: "direct_callback_remains_conservative",
			declaration: `execute(() -> {
      issueRequest();
    });`,
			wantHTTP: true,
		},
		{
			name: "anonymous_initializer_is_eager",
			declaration: `Runnable callback = new Runnable() {
      {
        issueRequest();
      }
      public void run() {
        deferredRequest();
      }
    };`,
			wantHTTP: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import java.net.http.HttpClient;
public class Actions {
  private void issueRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  private void deferredRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  private void execute(Runnable callback) {}
  public int action() {
    ` + tc.declaration + `
    return 0;
  }
}`,
			}
			program := mustParseJavaTestProgram(t, files)
			profile := classifyJavaArchive(program, nil, files)
			if got := profile.Surfaces[javaSurfaceHTTP]; got != tc.wantHTTP {
				t.Fatalf("HTTP surface = %v, want %v; evidence:\n%s", got, tc.wantHTTP, javaExecutableEvidenceText(program))
			}
		})
	}
}

func TestJavaClassifierIncludesConstructionAndSuperclassInitialization(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		surface javaSurface
	}{
		{
			name: "bare_constructed_helper_constructor",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() { new Helper(); return 0; }
}`,
				"Helper.java": `import java.net.http.HttpClient;
public class Helper {
  public Helper() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  private void noop() {}
}`,
			},
			surface: javaSurfaceHTTP,
		},
		{
			name: "constructed_helper_static_initializer",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() { new Helper(); return 0; }
}`,
				"Helper.java": `import javax.jms.MessageConsumer;
public class Helper {
  static {
    MessageConsumer consumer = suppliedConsumer;
    consumer.receive();
  }
  private void noop() {}
}`,
			},
			surface: javaSurfaceJMS,
		},
		{
			name: "archived_superclass_constructor",
			files: map[string]string{
				"Actions.java": `public class Actions extends Base {
  public int action() { return 0; }
}`,
				"Base.java": `import java.sql.Connection;
public class Base {
  protected Base() throws Exception {
    Connection connection = suppliedConnection;
    connection.commit();
  }
  private void noop() {}
}`,
			},
			surface: javaSurfaceDatabase,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			program := mustParseJavaTestProgram(t, tc.files)
			profile := classifyJavaArchive(program, nil, tc.files)
			if !profile.Surfaces[tc.surface] {
				t.Fatalf("surfaces = %#v, want %q; evidence:\n%s", profile.Surfaces, tc.surface, javaExecutableEvidenceText(program))
			}
		})
	}

	t.Run("subclass_override_shadows_inherited_helper", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `public class Actions extends Base {
  protected void issueRequest() {}
  public int action() {
    issueRequest();
    return 0;
  }
}`,
			"Base.java": `import java.net.http.HttpClient;
public class Base {
  protected void issueRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
}`,
		}
		profile := classifyJavaTestProfile(t, files)
		if profile.Surfaces[javaSurfaceHTTP] {
			t.Fatalf("surfaces = %#v, overridden superclass helper must not execute", profile.Surfaces)
		}
	})
}

func TestJavaClassifierResolvesInheritedHelpersAndFields(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		surface javaSurface
	}{
		{
			name: "unqualified_inherited_helper",
			files: map[string]string{
				"Actions.java": `public class Actions extends Base {
  public int action() throws Exception {
    issueRequest();
    return 0;
  }
}`,
				"Base.java": `import java.net.http.HttpClient;
public class Base {
  protected void issueRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
}`,
			},
			surface: javaSurfaceHTTP,
		},
		{
			name: "explicit_super_helper",
			files: map[string]string{
				"Actions.java": `public class Actions extends Base {
  private void commitWork() {}
  public int action() throws Exception {
    super.commitWork();
    return 0;
  }
}`,
				"Base.java": `import java.sql.Connection;
public class Base {
  protected void commitWork() throws Exception {
    Connection connection = suppliedConnection;
    connection.commit();
  }
}`,
			},
			surface: javaSurfaceDatabase,
		},
		{
			name: "inherited_protocol_field",
			files: map[string]string{
				"Actions.java": `public class Actions extends Base {
  public int action() {
    consumer.receive();
    return 0;
  }
}`,
				"Base.java": `import javax.jms.JMSConsumer;
public class Base {
  protected JMSConsumer consumer;
  protected void noop() {}
}`,
			},
			surface: javaSurfaceJMS,
		},
		{
			name: "inherited_archived_receiver_field",
			files: map[string]string{
				"Actions.java": `public class Actions extends Base {
  public int action() throws Exception {
    helper.run();
    return 0;
  }
}`,
				"Base.java": `import live.Helper;
public class Base {
  protected Helper helper;
  protected void noop() {}
}`,
				"live/Helper.java": `package live;
import java.net.http.HttpClient;
public class Helper {
  public void run() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
}`,
			},
			surface: javaSurfaceHTTP,
		},
		{
			name: "explicit_super_field_ignores_subclass_shadow",
			files: map[string]string{
				"Actions.java": `public class Actions extends Base {
  private CustomConsumer consumer;
  public int action() {
    super.consumer.receive();
    return 0;
  }
}`,
				"Base.java": `import javax.jms.JMSConsumer;
public class Base {
  protected JMSConsumer consumer;
  protected void noop() {}
}`,
				"CustomConsumer.java": `public class CustomConsumer {
  public void receive() {}
}`,
			},
			surface: javaSurfaceJMS,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			program := mustParseJavaTestProgram(t, tc.files)
			profile := classifyJavaArchive(program, nil, tc.files)
			if !profile.Surfaces[tc.surface] {
				t.Fatalf("surfaces = %#v, want %q; evidence:\n%s", profile.Surfaces, tc.surface, javaExecutableEvidenceText(program))
			}
		})
	}
}

func TestJavaClassifierSeparatesStaticAndInstanceInitialization(t *testing.T) {
	t.Run("static_call_does_not_construct_instance", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `public class Actions {
  public int action() {
    Helper.ping();
    return 0;
  }
}`,
			"Helper.java": `import java.net.http.HttpClient;
public class Helper {
  {
    issueRequest();
  }
  private void issueRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  public static void ping() {}
}`,
		}
		profile := classifyJavaTestProfile(t, files)
		if profile.Surfaces[javaSurfaceHTTP] {
			t.Fatalf("surfaces = %#v, static call must not execute instance initialization", profile.Surfaces)
		}
	})

	t.Run("static_call_executes_static_initializer", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `public class Actions {
  public int action() {
    Helper.ping();
    return 0;
  }
}`,
			"Helper.java": `import javax.jms.JMSConsumer;
public class Helper {
  static {
    JMSConsumer consumer = suppliedConsumer;
    consumer.receive();
  }
  public static void ping() {}
}`,
		}
		profile := classifyJavaTestProfile(t, files)
		if !profile.Surfaces[javaSurfaceJMS] {
			t.Fatalf("surfaces = %#v, static initializer must execute before static method", profile.Surfaces)
		}
	})
}

func TestJavaClassifierDefersReturnedAndYieldedLambdas(t *testing.T) {
	tests := []struct {
		name        string
		factoryBody string
	}{
		{
			name: "returned_block_lambda",
			factoryBody: `return () -> {
      issueRequest();
    };`,
		},
		{
			name:        "returned_expression_lambda",
			factoryBody: `return () -> issueRequest();`,
		},
		{
			name: "returned_ternary_lambdas",
			factoryBody: `return enabled
      ? () -> issueRequest()
      : () -> issueRequest();`,
		},
		{
			name: "yielded_lambda",
			factoryBody: `return switch (mode) {
      default -> {
        yield () -> issueRequest();
      }
    };`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import java.net.http.HttpClient;
public class Actions {
  private Runnable callbackFactory() {
    ` + tc.factoryBody + `
  }
  private void issueRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  public int action() {
    callbackFactory();
    return 0;
  }
}`,
			}
			program := mustParseJavaTestProgram(t, files)
			profile := classifyJavaArchive(program, nil, files)
			if profile.Surfaces[javaSurfaceHTTP] {
				t.Fatalf("surfaces = %#v, stored callback body must be deferred; evidence:\n%s", profile.Surfaces, javaExecutableEvidenceText(program))
			}
		})
	}
}

func TestJavaClassifierMasksEveryReservedEvidenceMarker(t *testing.T) {
	for _, marker := range []string{
		strings.TrimSuffix(javaProtocolScopeBoundary, ";"),
		strings.TrimSuffix(javaProtocolLocalScopeBoundary, ";"),
		strings.TrimSuffix(javaProtocolBlockStart, ";"),
		strings.TrimSuffix(javaProtocolBlockEnd, ";"),
		"__apexload_protocol_type_statement_jdbcstatement__",
		javaProtocolSuperFieldEvidenceName("statement"),
	} {
		t.Run(marker, func(t *testing.T) {
			if evidence := javaEvidenceCode("int " + marker + ";"); strings.Contains(strings.ToLower(evidence), strings.ToLower(marker)) {
				t.Fatalf("javaEvidenceCode retained reserved marker %q in %q", marker, evidence)
			}
		})
	}

	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    int __apexload_protocol_scope_boundary__;
    Statement statement = suppliedStatement;
    statement.execute(task);
    return 0;
  }
}`,
		"Statement.java": `public class Statement {
  public void execute(Object task) {}
}`,
	}
	profile := classifyJavaTestProfile(t, files)
	if profile.Surfaces[javaSurfaceDatabase] {
		t.Fatalf("surfaces = %#v, uploaded scope marker forged database evidence", profile.Surfaces)
	}
}

func TestJavaClassifierUnknownLRUsesRuntimeReachability(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    new Helper();
    return 0;
  }
}`,
		"Helper.java": `import lrapi.lr;
public class Helper {
  public Helper() {
    lr.future_runtime_call("value");
  }
  private void noop() {}
}`,
	}
	profile := classifyJavaTestProfile(t, files)
	if !containsString(profile.ReviewFamilies, "unrecognized_lr_method") {
		t.Fatalf("review families = %#v, want unrecognized_lr_method", profile.ReviewFamilies)
	}
}

func TestJavaClassifierRecognizesJMS20AndDomainFactories(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		call     string
	}{
		{name: "jms_producer", typeName: "JMSProducer", call: "subject.send(message);"},
		{name: "jms_consumer", typeName: "JMSConsumer", call: "subject.receive();"},
		{name: "queue_connection_factory", typeName: "QueueConnectionFactory", call: "subject.createConnection();"},
		{name: "topic_connection_factory", typeName: "TopicConnectionFactory", call: "subject.createConnection();"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import javax.jms.` + tc.typeName + `;
public class Actions {
  private ` + tc.typeName + ` subject;
  public int action() {
    ` + tc.call + `
    return 0;
  }
}`,
			}
			profile := classifyJavaTestProfile(t, files)
			if !profile.Surfaces[javaSurfaceJMS] {
				t.Fatalf("surfaces = %#v, want JMS for %s", profile.Surfaces, tc.typeName)
			}
		})
	}
}

func TestJavaClassifierConservativelyTraversesUnknownSameArityOverloads(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import java.net.http.HttpClient;
public class Actions {
  private Object runtimeValue() { return null; }
  private void dispatch(String value) throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  private void dispatch(Integer value) {}
  public int action() throws Exception {
    dispatch(runtimeValue());
    return 0;
  }
}`,
	}
	program := mustParseJavaTestProgram(t, files)
	profile := classifyJavaArchive(program, nil, files)
	if !profile.Surfaces[javaSurfaceHTTP] {
		t.Fatalf("surfaces = %#v, unresolved overload must conservatively retain HTTP candidate; evidence:\n%s", profile.Surfaces, javaExecutableEvidenceText(program))
	}
}

func TestJavaClassifierResolvesArchivedStaticImportedHelpers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		importLine string
	}{
		{name: "exact", importLine: "import static support.Helper.issueRequest;"},
		{name: "wildcard", importLine: "import static support.Helper.*;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": tc.importLine + `
public class Actions {
  public int action() throws Exception {
    issueRequest();
    return 0;
  }
}`,
				"support/Helper.java": `package support;
import java.net.http.HttpClient;
import lrapi.lr;
public class Helper {
  public static void issueRequest() throws Exception {
    lr.decrypt("review-only");
    HttpClient.newHttpClient().send(request, handler);
  }
}`,
			}
			program := mustParseJavaTestProgram(t, files)
			profile := classifyJavaArchive(program, nil, files)
			if !profile.Surfaces[javaSurfaceHTTP] {
				t.Fatalf("surfaces = %#v, static-imported helper must contribute HTTP evidence", profile.Surfaces)
			}
			blockers := strings.Join(
				javaVuserRunnerSemanticBlockers(buildJavaVuserRuntimeReachability(program)),
				"\n",
			)
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, static-imported helper must be runtime-reachable", blockers)
			}
		})
	}
}

func TestJavaClassifierModelsArchivedVirtualDispatch(t *testing.T) {
	t.Run("declared_base_receiver_reaches_subtype_override", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `public class Actions {
  public int action() throws Exception {
    Base worker = new Derived();
    worker.run();
    return 0;
  }
}`,
			"Base.java": `public class Base {
  public void run() {}
}`,
			"Derived.java": `import java.net.http.HttpClient;
import lrapi.lr;
public class Derived extends Base {
  public void run() throws Exception {
    lr.decrypt("virtual-override");
    HttpClient.newHttpClient().send(request, handler);
  }
}`,
		}
		program := mustParseJavaTestProgram(t, files)
		profile := classifyJavaArchive(program, nil, files)
		if !profile.Surfaces[javaSurfaceHTTP] {
			t.Fatalf("surfaces = %#v, subtype override must be reachable", profile.Surfaces)
		}
		blockers := strings.Join(
			javaVuserRunnerSemanticBlockers(buildJavaVuserRuntimeReachability(program)),
			"\n",
		)
		if !strings.Contains(blockers, "lr_decrypt") {
			t.Fatalf("runner semantic blockers = %q, subtype override must reach lr_decrypt", blockers)
		}
	})

	t.Run("base_template_dispatches_to_actions_override", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `import java.net.http.HttpClient;
public class Actions extends Base {
  protected void hook() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  public int action() throws Exception {
    invokeHook();
    return 0;
  }
}`,
			"Base.java": `public class Base {
  protected void hook() {}
  protected void invokeHook() throws Exception { hook(); }
}`,
		}
		if profile := classifyJavaTestProfile(t, files); !profile.Surfaces[javaSurfaceHTTP] {
			t.Fatalf("surfaces = %#v, virtual template hook must reach Actions override", profile.Surfaces)
		}
	})
}

func TestJavaClassifierKeepsNonVirtualCallsNonVirtual(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "explicit_super",
			files: map[string]string{
				"Actions.java": `import java.net.http.HttpClient;
public class Actions extends Base {
  public void run() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  public int action() throws Exception {
    super.run();
    return 0;
  }
}`,
				"Base.java": `public class Base {
  public void run() {}
}`,
			},
		},
		{
			name: "final_method",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    Base worker = new Derived();
    worker.run();
    return 0;
  }
}`,
				"Base.java": `public class Base {
  public final void run() {}
}`,
				"Derived.java": `import java.net.http.HttpClient;
public class Derived extends Base {
  public void run() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
}`,
			},
		},
		{
			name: "static_method",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    Base worker = new Derived();
    worker.run();
    return 0;
  }
}`,
				"Base.java": `public class Base {
  public static void run() {}
}`,
				"Derived.java": `import java.net.http.HttpClient;
public class Derived extends Base {
  public static void run() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
}`,
			},
		},
		{
			name: "private_method",
			files: map[string]string{
				"Actions.java": `public class Actions {
  public int action() {
    new Derived().invoke();
    return 0;
  }
}`,
				"Base.java": `public class Base {
  private void run() {}
  public void invoke() { run(); }
}`,
				"Derived.java": `import java.net.http.HttpClient;
public class Derived extends Base {
  public void run() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
}`,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if profile := classifyJavaTestProfile(t, tc.files); profile.Surfaces[javaSurfaceHTTP] {
				t.Fatalf("surfaces = %#v, non-virtual call reached subtype implementation", profile.Surfaces)
			}
		})
	}
}

func TestJavaClassifierReconnectsInvokedStoredFunctionalValues(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		actionBody string
	}{
		{
			name: "block_lambda",
			actionBody: `Runnable callback = () -> {
      issueRequest();
    };
    callback.run();`,
		},
		{
			name: "expression_lambda",
			actionBody: `Runnable callback = () -> issueRequest();
    callback.run();`,
		},
		{
			name: "method_reference",
			actionBody: `Runnable callback = this::issueRequest;
    callback.run();`,
		},
		{
			name: "anonymous_functional_method",
			actionBody: `Runnable callback = new Runnable() {
      public void run() {
        issueRequest();
      }
    };
    callback.run();`,
		},
		{
			name:       "invoked_field_lambda",
			field:      `private final Runnable callback = () -> issueRequest();`,
			actionBody: `callback.run();`,
		},
		{
			name: "callable_call",
			actionBody: `java.util.concurrent.Callable<Integer> callback = () -> {
      issueRequest();
      return 1;
    };
    callback.call();`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import java.net.http.HttpClient;
import lrapi.lr;
public class Actions {
  ` + tc.field + `
  private void issueRequest() throws Exception {
    lr.decrypt("invoked-functional");
    HttpClient.newHttpClient().send(request, handler);
  }
  public int action() throws Exception {
    ` + tc.actionBody + `
    return 0;
  }
}`,
			}
			program := mustParseJavaTestProgram(t, files)
			profile := classifyJavaArchive(program, nil, files)
			if !profile.Surfaces[javaSurfaceHTTP] {
				t.Fatalf("surfaces = %#v, invoked functional body must be reachable; evidence:\n%s", profile.Surfaces, javaExecutableEvidenceText(program))
			}
			blockers := strings.Join(
				javaVuserRunnerSemanticBlockers(buildJavaVuserRuntimeReachability(program)),
				"\n",
			)
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, invoked functional body must reach lr_decrypt", blockers)
			}
		})
	}
}

func TestJavaClassifierKeepsUninvokedFunctionalValuesDeferred(t *testing.T) {
	tests := []struct {
		name       string
		actionBody string
	}{
		{
			name:       "method_reference_never_invoked",
			actionBody: `Runnable callback = this::issueRequest;`,
		},
		{
			name: "lambda_object_method_is_not_sam_invocation",
			actionBody: `Runnable callback = () -> issueRequest();
    callback.toString();`,
		},
		{
			name: "anonymous_other_method_is_not_functional_invocation",
			actionBody: `Runnable callback = new Runnable() {
      public void run() {
        issueRequest();
      }
    };
    callback.toString();`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import java.net.http.HttpClient;
public class Actions {
  private void issueRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  public int action() {
    ` + tc.actionBody + `
    return 0;
  }
}`,
			}
			profile := classifyJavaTestProfile(t, files)
			if profile.Surfaces[javaSurfaceHTTP] {
				t.Fatalf("surfaces = %#v, uninvoked functional body must stay deferred", profile.Surfaces)
			}
		})
	}

	t.Run("functional_parameter_shadows_field", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `import java.net.http.HttpClient;
public class Actions {
  private final Runnable callback = () -> issueRequest();
  private void issueRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  private void invoke(Runnable callback) {
    callback.run();
  }
  public int action() {
    invoke(() -> {});
    return 0;
  }
}`,
		}
		if profile := classifyJavaTestProfile(t, files); profile.Surfaces[javaSurfaceHTTP] {
			t.Fatalf("surfaces = %#v, functional parameter must shadow stored field callback", profile.Surfaces)
		}
	})

	t.Run("reassignment_replaces_stored_field_target", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `import java.net.http.HttpClient;
public class Actions {
  private Runnable callback = () -> issueRequest();
  private Runnable supplied;
  private void issueRequest() throws Exception {
    HttpClient.newHttpClient().send(request, handler);
  }
  public int action() {
    callback = supplied;
    callback.run();
    return 0;
  }
}`,
		}
		if profile := classifyJavaTestProfile(t, files); profile.Surfaces[javaSurfaceHTTP] {
			t.Fatalf("surfaces = %#v, reassignment must replace stored field callback target", profile.Surfaces)
		}
	})
}

func TestJavaClassifierFailsClosedForUnresolvedInvokedFunctionalValue(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  private Runnable callback;
  public int action() {
    callback.run();
    return 0;
  }
}`,
	}
	program := mustParseJavaTestProgram(t, files)
	owner, ok := javaVuserClass(program)
	if !ok {
		t.Fatal("missing Actions class")
	}
	action, ok := javaVuserLifecycleMethod(program, "action")
	if !ok {
		t.Fatal("missing action")
	}
	index := newJavaEvidenceIndex(program)
	unresolved := javaUnresolvedInvokedFunctionalValues(
		action,
		action.Statements[0].Text,
		owner,
		index,
		javaDeclaredVariableTypesWithHierarchy(index, owner, action),
	)
	if len(unresolved) != 1 || unresolved[0] != "callback" {
		t.Fatalf("unresolved functional values = %#v, want [callback]", unresolved)
	}

	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "functional value callback") {
		t.Fatalf("runner semantic blockers = %q, unresolved invoked callback must block promotion", blockers)
	}
}

func TestJavaClassifierFailsClosedForUnresolvedProgramTypes(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "default_package_external_classpath",
			source: `public class Actions {
  private HttpClient client;
  public int action() { client.send(request, handler); return 0; }
}`,
		},
		{
			name: "same_package_external_classpath",
			source: `package app;
public class Actions {
  private Statement statement;
  public int action() { statement.execute(sql); return 0; }
}`,
		},
		{
			name: "external_wildcard_can_shadow_standard_name",
			source: `package app;
import vendor.client.*;
public class Actions {
  private MessageConsumer consumer;
  public int action() { consumer.receive(); return 0; }
}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"Actions.java": tc.source}
			profile := classifyJavaArchive(mustParseJavaTestProgram(t, files), nil, files)
			for _, surface := range []javaSurface{javaSurfaceHTTP, javaSurfaceDatabase, javaSurfaceJMS, javaSurfaceSOAP} {
				if profile.Surfaces[surface] {
					t.Fatalf("surfaces = %#v, unresolved type must not imply %q", profile.Surfaces, surface)
				}
			}
		})
	}
}

func TestJavaClassifierIgnoresForgedTypeBindingIdentifiers(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  private Statement statement;
  private void __apexload_protocol_type_statement_jdbcstatement__() {}
  public int action() {
    __apexload_protocol_type_statement_jdbcstatement__();
    statement.execute(task);
    return 0;
  }
}`,
		"Statement.java": `public class Statement {
  public void execute(Object task) {}
}`,
	}
	profile := classifyJavaArchive(mustParseJavaTestProgram(t, files), nil, files)
	if profile.Surfaces[javaSurfaceDatabase] {
		t.Fatalf("surfaces = %#v, uploaded identifier forged a JDBC binding", profile.Surfaces)
	}
}

func TestJavaClassifierProviderContextIsReachableOnly(t *testing.T) {
	t.Run("unused_service_bus_backup_does_not_change_jms_provider", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `import javax.jms.MessageConsumer;
public class Actions {
  private MessageConsumer consumer;
  public int action() { consumer.receive(); return 0; }
}`,
			"Unused.java": `public class Unused {
  private String provider = "azure.servicebus";
  private void noop() {}
}`,
		}
		profile := classifyJavaArchive(mustParseJavaTestProgram(t, files), nil, files)
		if profile.JMSProvider != "solace" || profile.JMSProtocol != "jms-solace" {
			t.Fatalf("provider/protocol = %q/%q, want solace/jms-solace", profile.JMSProvider, profile.JMSProtocol)
		}
	})

	t.Run("unused_postgres_backup_does_not_change_oracle_driver", func(t *testing.T) {
		files := map[string]string{
			"Actions.java": `import java.sql.Connection;
public class Actions {
  private Connection connection;
  private String url = "jdbc:oracle:thin:@database";
  public int action() throws Exception { connection.commit(); return 0; }
}`,
			"Unused.java": `public class Unused {
  private String url = "jdbc:postgresql://backup";
  private void noop() {}
}`,
		}
		profile := classifyJavaArchive(mustParseJavaTestProgram(t, files), nil, files)
		if profile.DatabaseDriver != "oracle" {
			t.Fatalf("database driver = %q, want oracle", profile.DatabaseDriver)
		}
	})
}

func TestJavaEvidenceIndexBoundsLargeArchiveResolution(t *testing.T) {
	const classCount = 4096
	classes := make([]javaClass, 0, classCount)
	for index := 0; index < classCount; index++ {
		name := fmt.Sprintf("Type%04d", index)
		classes = append(classes, javaClass{
			File:    "p/" + name + ".java",
			Name:    name,
			Package: "p",
		})
	}
	evidenceIndex := newJavaEvidenceIndex(javaProgram{Classes: classes})
	if got := len(evidenceIndex.qualified); got != classCount {
		t.Fatalf("qualified index size = %d, want %d", got, classCount)
	}
	for iteration := 0; iteration < classCount; iteration++ {
		resolved, ok := evidenceIndex.classForType(classes[0], "p.Type4095")
		if !ok || resolved.Name != "Type4095" {
			t.Fatalf("lookup %d resolved %#v, %v", iteration, resolved, ok)
		}
	}
	_ = javaProtocolTypeBindingEvidence(evidenceIndex, classes[0])
	_ = javaProtocolTypeBindingEvidence(evidenceIndex, classes[0])
	if got := len(evidenceIndex.bindingCache); got != 1 {
		t.Fatalf("binding cache entries = %d, want 1", got)
	}
}

func TestJavaEvidenceIndexCachesImportResolutionBySourceScope(t *testing.T) {
	imports := []string{
		"p.Helper",
		"static p.Utilities.work",
		"java.net.http.HttpClient",
	}
	actions := javaClass{
		File:    "p/Actions.java",
		Name:    "Actions",
		Package: "p",
		Imports: imports,
	}
	companion := javaClass{
		File:    "p/Actions.java",
		Name:    "Companion",
		Package: "p",
		Imports: imports,
	}
	helper := javaClass{File: "p/Helper.java", Name: "Helper", Package: "p"}
	utilityMethod := javaMethod{
		Name:               "work",
		File:               "p/Utilities.java",
		StartLine:          2,
		Visibility:         "public",
		Static:             true,
		ParameterSignature: "",
	}
	utilities := javaClass{
		File:    "p/Utilities.java",
		Name:    "Utilities",
		Package: "p",
		Methods: map[string]javaMethod{
			"work": utilityMethod,
		},
		MethodOverloads: map[string][]javaMethod{
			"work": {utilityMethod},
		},
	}
	index := newJavaEvidenceIndex(javaProgram{
		Classes: []javaClass{actions, companion, helper, utilities},
	})
	for iteration := 0; iteration < 4096; iteration++ {
		if resolved, ok := index.classForType(actions, "Helper"); !ok ||
			resolved.Name != "Helper" {
			t.Fatalf("classForType iteration %d = %#v, %t", iteration, resolved, ok)
		}
		methods := javaEvidenceStaticImportedMethods(index, actions, "work")
		if len(methods) != 1 || methods[0].method.Name != "work" {
			t.Fatalf("static methods iteration %d = %#v", iteration, methods)
		}
	}
	_ = javaProtocolTypeBindingEvidence(index, actions)
	_ = javaProtocolTypeBindingEvidence(index, companion)
	if got := len(index.importScopes); got != 1 {
		t.Fatalf("import scopes = %d, want one shared file/package scope", got)
	}
	if got := len(index.staticMethodCache); got != 1 {
		t.Fatalf("static method cache entries = %d, want 1", got)
	}
	if got := len(index.bindingCache); got != 1 {
		t.Fatalf("protocol binding cache entries = %d, want 1", got)
	}
}

func TestConvertJavaPreservesCapturedStoredFunctionalReceiverTypes(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    Helper helper = new Helper();
    Runnable callback = () -> helper.danger();
    callback.run();
    return 0;
  }
}`,
		"Helper.java": `import lrapi.lr;
public class Helper {
  public void danger() {
    lr.decrypt("captured-receiver");
  }
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, captured Helper receiver must remain typed", blockers)
	}
}

func TestConvertJavaExecutesStoredCallbacksPassedToKnownAPIs(t *testing.T) {
	tests := []struct {
		name       string
		imports    string
		field      string
		actionBody string
	}{
		{
			name:    "executor_execute",
			imports: "import java.util.concurrent.Executor;",
			field:   "private Executor executor;",
			actionBody: `Runnable callback = () -> lr.decrypt("executor-execute");
    executor.execute(callback);`,
		},
		{
			name:    "executor_service_submit",
			imports: "import java.util.concurrent.ExecutorService;",
			field:   "private ExecutorService executor;",
			actionBody: `Runnable callback = () -> lr.decrypt("executor-submit");
    executor.submit(callback);`,
		},
		{
			name:    "iterable_for_each",
			imports: "import java.util.List;\nimport java.util.function.Consumer;",
			field:   "private List<String> items;",
			actionBody: `Consumer<String> callback = value -> lr.decrypt("for-each");
    items.forEach(callback);`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": tc.imports + `
import lrapi.lr;
public class Actions {
  ` + tc.field + `
  public int action() {
    ` + tc.actionBody + `
    return 0;
  }
}`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, known execution API must reach callback", blockers)
			}
		})
	}
}

func TestJavaReturnedReceiverCallsIgnoreNestedArgumentProducers(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		producer string
		ops      []string
	}{
		{
			name:     "nested_call",
			source:   `wrap(inner()).danger();`,
			producer: "wrap",
		},
		{
			name:     "extra_parentheses",
			source:   `return ((wrap(inner()))).danger();`,
			producer: "wrap",
		},
		{
			name:     "cast_receiver",
			source:   `return ((Outer) wrap(inner())).danger();`,
			producer: "wrap",
			ops:      []string{"cast:Outer"},
		},
		{
			name:     "array_receiver",
			source:   `return wrapArray(inner())[0].danger();`,
			producer: "wrapArray",
			ops:      []string{"array"},
		},
		{
			name:     "cast_then_array_receiver",
			source:   `return ((Outer[]) wrapArray(inner()))[0].danger();`,
			producer: "wrapArray",
			ops:      []string{"cast:Outer[]", "array"},
		},
		{
			name:     "array_then_cast_receiver",
			source:   `return ((Outer) wrapArray(inner())[0]).danger();`,
			producer: "wrapArray",
			ops:      []string{"array", "cast:Outer"},
		},
		{
			name:     "direct_grouped_receiver",
			source:   `return ((inner())).danger();`,
			producer: "inner",
		},
		{
			name:     "nested_cast_array_argument",
			source:   `return wrap(((Inner[]) inners())[0]).danger();`,
			producer: "wrap",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := javaReturnedReceiverCalls(tc.source)
			if len(calls) != 1 {
				t.Fatalf(
					"returned receiver calls = %#v, want only outer producer",
					calls,
				)
			}
			call := calls[0]
			gotOps := make([]string, 0, len(call.receiverOperations))
			for _, operation := range call.receiverOperations {
				switch operation.kind {
				case javaReturnedReceiverCast:
					gotOps = append(gotOps, "cast:"+operation.castType)
				case javaReturnedReceiverArrayElement:
					gotOps = append(gotOps, "array")
				}
			}
			if call.producerName != tc.producer ||
				strings.Join(gotOps, ",") != strings.Join(tc.ops, ",") ||
				call.methodName != "danger" {
				t.Fatalf(
					"returned receiver call = %#v ops=%v, want producer=%q ops=%v method=danger",
					call,
					gotOps,
					tc.producer,
					tc.ops,
				)
			}
		})
	}
}

func TestConvertJavaReturnedReceiverUsesOnlyOuterExpressionResult(t *testing.T) {
	tests := []struct {
		name       string
		expression string
	}{
		{name: "nested_call", expression: `wrap(inner()).danger();`},
		{name: "extra_parentheses", expression: `((wrap(inner()))).danger();`},
		{name: "cast_receiver", expression: `((Outer) wrap(inner())).danger();`},
		{name: "array_receiver", expression: `wrapArray(inner())[0].danger();`},
		{
			name:       "cast_array_receiver",
			expression: `((Outer[]) wrapArray(inner()))[0].danger();`,
		},
		{
			name:       "array_cast_receiver",
			expression: `((Outer) wrapArray(inner())[0]).danger();`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    ` + tc.expression + `
    return 0;
  }
  private Inner inner() { return new Inner(); }
  private Outer wrap(Inner value) { return new Outer(); }
  private Outer[] wrapArray(Inner value) { return new Outer[]{new Outer()}; }
}
class Inner {
  public void danger() { lr.decrypt("nested-argument-must-not-run"); }
}
class Outer {
  public void danger() {
    lr.start_transaction("OuterResult");
    lr.end_transaction("OuterResult", lr.AUTO);
  }
}`,
			}
			program := mustParseJavaTestProgram(t, files)
			owner, ownerOK := javaVuserClass(program)
			action, actionOK := javaVuserLifecycleMethod(program, "action")
			if !ownerOK || !actionOK {
				t.Fatalf("selected owner/action missing from %#v", program)
			}
			index := newJavaEvidenceIndex(program)
			targets := javaResolvedEvidenceHelperCalls(
				tc.expression,
				owner,
				action,
				index,
				javaDeclaredVariableTypesWithHierarchy(
					index,
					owner,
					action,
				),
			)
			foundOuterDanger := false
			for _, target := range targets {
				if target.class.Name == "Outer" &&
					target.method.Name == "danger" {
					foundOuterDanger = true
				}
			}
			if !foundOuterDanger {
				t.Fatalf(
					"resolved targets = %#v, returned calls = %#v; outer danger missing",
					targets,
					javaReturnedReceiverCalls(tc.expression),
				)
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(
				javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
				"\n",
			)
			if strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf(
					"runner semantic blockers = %q, nested argument receiver ran",
					blockers,
				)
			}
			emits, _ := result.Script.Actions[0].
				Metadata["emits_business_transactions"].(bool)
			if !emits {
				t.Fatalf(
					"action metadata = %#v, outer result transaction missing",
					result.Script.Actions[0].Metadata,
				)
			}
		})
	}
}

func TestConvertJavaReturnedReceiverDoesNotPromoteNestedArgumentTransaction(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() {
    wrap(inner()).danger();
    return 0;
  }
  private Inner inner() { return new Inner(); }
  private Outer wrap(Inner value) { return new Outer(); }
}
class Inner {
  public void danger() {
    lr.start_transaction("NestedArgument");
    lr.end_transaction("NestedArgument", lr.AUTO);
  }
}
class Outer {
  public void danger() { lr.decrypt("outer-result"); }
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, outer result blocker missing", blockers)
	}
	emits, _ := result.Script.Actions[0].
		Metadata["emits_business_transactions"].(bool)
	if emits {
		t.Fatalf(
			"action metadata = %#v, nested argument transaction suppressed iteration transaction",
			result.Script.Actions[0].Metadata,
		)
	}
}

func TestConvertJavaKeepsCallbacksPassedToUnknownStorageDeferred(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  private Registry registry;
  public int action() {
    Runnable callback = () -> lr.decrypt("stored-only");
    registry.register(callback);
    return 0;
  }
}`,
		"Registry.java": `public class Registry {
  public void register(Runnable callback) {}
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if strings.Contains(blockers, "lr_decrypt") ||
		strings.Contains(blockers, "functional value callback") {
		t.Fatalf("runner semantic blockers = %q, unknown storage API must not execute callback", blockers)
	}
}

func TestConvertJavaFailsClosedForUnresolvedCallbackPassedToKnownAPI(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import java.util.concurrent.Executor;
public class Actions {
  private Executor executor;
  private Runnable callback;
  public int action() {
    executor.execute(callback);
    return 0;
  }
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "functional value callback") {
		t.Fatalf("runner semantic blockers = %q, unresolved Executor callback must block promotion", blockers)
	}
}

func TestConvertJavaUnionsStoredFunctionalControlFlowAlternatives(t *testing.T) {
	tests := []struct {
		name       string
		actionBody string
	}{
		{
			name: "identical_invocations_around_reassignment",
			actionBody: `Runnable callback = () -> {};
    callback.run();
    callback = () -> lr.decrypt("second-occurrence");
    callback.run();`,
		},
		{
			name: "branch_alternatives",
			actionBody: `Runnable callback = () -> {};
    if (flag) {
      callback = () -> lr.decrypt("branch-alternative");
    } else {
      callback = () -> {};
    }
    callback.run();`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  private boolean flag;
  public int action() {
    ` + tc.actionBody + `
    return 0;
  }
}`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, every reachable callback alternative must be retained", blockers)
			}
		})
	}
}

func TestConvertJavaResolvesArchivedInterfaceDispatch(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() {
    Worker worker = new Derived();
    worker.run();
    return 0;
  }
}`,
		"Worker.java": `public interface Worker {
  void run();
}`,
		"Derived.java": `import lrapi.lr;
public class Derived implements Worker {
  public void run() {
    lr.decrypt("interface-dispatch");
  }
}`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
	if !strings.Contains(blockers, "lr_decrypt") {
		t.Fatalf("runner semantic blockers = %q, interface receiver must reach archived implementation", blockers)
	}
}

func TestJavaFunctionalBindingTimelineBuildsOnceForLargeMethod(t *testing.T) {
	var body strings.Builder
	body.WriteString("Runnable callback = () -> {};\n")
	for index := 0; index < 512; index++ {
		body.WriteString("callback.run();\n")
	}
	files := map[string]string{
		"Actions.java": "public class Actions {\npublic int action() {\n" +
			body.String() + "return 0;\n}\n}",
	}
	program := mustParseJavaTestProgram(t, files)
	owner, ok := javaVuserClass(program)
	if !ok {
		t.Fatal("missing Actions class")
	}
	action, ok := javaVuserLifecycleMethod(program, "action")
	if !ok {
		t.Fatal("missing action")
	}
	index := newJavaEvidenceIndex(program)
	types := javaDeclaredVariableTypesWithHierarchy(index, owner, action)
	for _, statement := range action.Statements {
		javaInvokedFunctionalReachability(action, statement.Text, owner, index, types)
		collectJavaDeclaredVariableTypes(types, statement.Text)
	}
	if got := index.functionalBindingBuilds; got != 1 {
		t.Fatalf("functional binding timeline builds = %d, want 1 for one large method", got)
	}
	if got := len(index.functionalBindings); got != 1 {
		t.Fatalf("functional binding cache entries = %d, want 1", got)
	}
}

func TestJavaEvidenceIndexPrecomputesLargeSubtypeFanout(t *testing.T) {
	const subtypeCount = 1024
	baseMethod := javaMethod{
		Name:           "run",
		File:           "Base.java",
		Visibility:     "public",
		ParameterCount: 0,
	}
	base := javaClass{
		File:            "Base.java",
		Name:            "Base",
		Methods:         map[string]javaMethod{"run": baseMethod},
		MethodOverloads: map[string][]javaMethod{"run": {baseMethod}},
	}
	classes := []javaClass{base}
	for index := 0; index < subtypeCount; index++ {
		name := fmt.Sprintf("Derived%04d", index)
		override := javaMethod{
			Name:           "run",
			File:           name + ".java",
			Visibility:     "public",
			ParameterCount: 0,
		}
		classes = append(classes, javaClass{
			File:            name + ".java",
			Name:            name,
			SuperClass:      "Base",
			Methods:         map[string]javaMethod{"run": override},
			MethodOverloads: map[string][]javaMethod{"run": {override}},
		})
	}
	index := newJavaEvidenceIndex(javaProgram{Classes: classes})
	if got := len(index.subtypeIndicesFor(base)); got != subtypeCount {
		t.Fatalf("precomputed subtype fanout = %d, want %d", got, subtypeCount)
	}
	targets := javaEvidenceVirtualDispatchTargets(index, base, base, baseMethod)
	if got := len(targets); got != subtypeCount {
		t.Fatalf("virtual dispatch targets = %d, want %d", got, subtypeCount)
	}
}

func TestJavaScannerPreservesInterfaceInheritanceAndMethods(t *testing.T) {
	files := map[string]string{
		"A.java": `public interface A {
  void alpha();
}`,
		"B.java": `public interface B {
  void beta();
}`,
		"Child.java": `public interface Child extends A, B {
  default void run() {}
}`,
		"AbstractBase.java": `public abstract class AbstractBase {
  protected abstract void work();
}`,
	}
	program, warnings := parseJavaProgram(files)
	if len(warnings) != 0 {
		t.Fatalf("parse warnings = %v", warnings)
	}
	classes := make(map[string]javaClass)
	for _, class := range program.Classes {
		classes[class.Name] = class
	}
	child := classes["Child"]
	if child.SuperClass != "" {
		t.Fatalf("Child superclass = %q, interfaces must not use the class superclass slot", child.SuperClass)
	}
	if got := strings.Join(child.Interfaces, ","); got != "A,B" {
		t.Fatalf("Child interfaces = %q, want A,B", got)
	}
	runMethods := javaMethodsNamed(child, "run")
	if len(runMethods) != 1 || !runMethods[0].Default || runMethods[0].Abstract {
		t.Fatalf("Child.run methods = %#v, want one concrete default method", runMethods)
	}
	alphaMethods := javaMethodsNamed(classes["A"], "alpha")
	if len(alphaMethods) != 1 || !alphaMethods[0].Abstract {
		t.Fatalf("A.alpha methods = %#v, want one bodyless abstract declaration", alphaMethods)
	}
	workMethods := javaMethodsNamed(classes["AbstractBase"], "work")
	if len(workMethods) != 1 || !workMethods[0].Abstract {
		t.Fatalf("AbstractBase.work methods = %#v, want one bodyless abstract declaration", workMethods)
	}
}

func TestConvertJavaResolvesInheritedDefaultInterfaceMethods(t *testing.T) {
	tests := []struct {
		name        string
		actionsDecl string
		actionBody  string
		extra       string
	}{
		{
			name:        "class_typed_receiver",
			actionsDecl: `public class Actions`,
			actionBody: `Impl worker = new Impl();
    worker.run();`,
			extra: `class Impl implements Worker {
}`,
		},
		{
			name:        "unqualified_inherited_default",
			actionsDecl: `public class Actions implements Worker`,
			actionBody:  `run();`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": tc.actionsDecl + ` {
  public int action() {
    ` + tc.actionBody + `
    return 0;
  }
}
` + tc.extra,
				"Worker.java": `import lrapi.lr;
public interface Worker {
  default void run() {
    lr.decrypt("default-interface");
  }
}`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, inherited default method must be reachable", blockers)
			}
		})
	}
}

func TestJavaClassifierInitializesOnlyDefaultMethodSuperinterfaces(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions implements WithDefault, WithoutDefault {
  public int action() {
    return 0;
  }
}`,
		"WithDefault.java": `public interface WithDefault {
  Object VALUE = Helper.withDefault();
  default void marker() {}
}`,
		"WithoutDefault.java": `public interface WithoutDefault {
  Object VALUE = Helper.withoutDefault();
  void marker();
}`,
		"Helper.java": `import lrapi.lr;
public class Helper {
  public static Object withDefault() {
    lr.decrypt("initialized-default-interface");
    return null;
  }
  public static Object withoutDefault() {
    lr.decrypt_ex("must-stay-uninitialized", "unused");
    return null;
  }
}`,
	}
	program := mustParseJavaTestProgram(t, files)
	evidence := javaExecutableEvidenceText(program)
	if !strings.Contains(evidence, "lr.decrypt(") {
		t.Fatalf("evidence misses initialized default-method superinterface:\n%s", evidence)
	}
	if strings.Contains(evidence, "lr.decrypt_ex(") {
		t.Fatalf("evidence initialized interface without a default method:\n%s", evidence)
	}
}

func TestJavaEvidenceVirtualDispatchBoundsManyDeepReceivers(t *testing.T) {
	const classCount = 4096
	classes := make([]javaClass, 0, classCount)
	baseMethod := javaMethod{
		Name:           "run",
		File:           "Type0000.java",
		Visibility:     "public",
		ParameterCount: 0,
	}
	for classIndex := 0; classIndex < classCount; classIndex++ {
		name := fmt.Sprintf("Type%04d", classIndex)
		class := javaClass{
			File:            name + ".java",
			Name:            name,
			Methods:         map[string]javaMethod{},
			MethodOverloads: map[string][]javaMethod{},
		}
		if classIndex > 0 {
			class.SuperClass = fmt.Sprintf("Type%04d", classIndex-1)
		}
		if classIndex == 0 {
			class.Methods["run"] = baseMethod
			class.MethodOverloads["run"] = []javaMethod{baseMethod}
		}
		if classIndex == classCount-1 {
			override := javaMethod{
				Name:           "run",
				File:           name + ".java",
				Visibility:     "public",
				ParameterCount: 0,
			}
			class.Methods["run"] = override
			class.MethodOverloads["run"] = []javaMethod{override}
		}
		classes = append(classes, class)
	}
	index := newJavaEvidenceIndex(javaProgram{Classes: classes})
	if got := len(index.directSubtypes); got != classCount-1 {
		t.Fatalf("direct subtype keys = %d, want %d", got, classCount-1)
	}
	if got := len(index.methodClasses["run"]); got != 2 {
		t.Fatalf("run method candidate classes = %d, want 2", got)
	}
	for receiverIndex := 0; receiverIndex < classCount-1; receiverIndex++ {
		targets := javaEvidenceVirtualDispatchTargets(
			index,
			classes[receiverIndex],
			classes[0],
			baseMethod,
		)
		if len(targets) != 1 || targets[0].class.Name != "Type4095" {
			t.Fatalf("receiver %d targets = %#v, want leaf override", receiverIndex, targets)
		}
	}
	if got := len(index.subtypeCache); got != 0 {
		t.Fatalf("class dispatch populated %d transitive subtype caches, want 0", got)
	}
	if got := len(index.classIntervals); got != classCount {
		t.Fatalf("class interval count = %d, want %d", got, classCount)
	}
}

func TestJavaFunctionalBindingTimelineBoundsManyBindings(t *testing.T) {
	const callbackCount = 4096
	var body strings.Builder
	for callbackIndex := 0; callbackIndex < callbackCount; callbackIndex++ {
		name := fmt.Sprintf("callback%04d", callbackIndex)
		body.WriteString("Runnable ")
		body.WriteString(name)
		body.WriteString(" = () -> {};\n")
		body.WriteString(name)
		body.WriteString(".run();\n")
	}
	body.WriteString("return 0;\n")
	program := mustParseJavaTestProgram(t, map[string]string{
		"Actions.java": "public class Actions {\npublic int action() {\n" +
			body.String() + "}\n}",
	})
	owner, ok := javaVuserClass(program)
	if !ok {
		t.Fatal("missing Actions class")
	}
	action, ok := javaVuserLifecycleMethod(program, "action")
	if !ok {
		t.Fatal("missing action")
	}
	index := newJavaEvidenceIndex(program)
	lastCall := action.Statements[len(action.Statements)-2].Text
	javaStoredFunctionalBindings(action, owner, index, lastCall)
	if index.functionalBindingBuilds != 1 {
		t.Fatalf("functional timeline builds = %d, want 1", index.functionalBindingBuilds)
	}
	cacheKey := javaEvidenceOwnerKey(owner) + "\x00" + javaEvidenceMethodIdentity(action)
	timeline := index.functionalBindings[cacheKey]
	bindingNames := 0
	bindingAlternatives := 0
	for _, bindings := range timeline {
		bindingNames += len(bindings)
		for _, alternatives := range bindings {
			bindingAlternatives += len(alternatives)
		}
	}
	if bindingNames != callbackCount || bindingAlternatives != callbackCount {
		t.Fatalf(
			"timeline names/alternatives = %d/%d, want %d/%d",
			bindingNames,
			bindingAlternatives,
			callbackCount,
			callbackCount,
		)
	}
}

func TestConvertJavaResolvesExpressionHelperReceivers(t *testing.T) {
	tests := []struct {
		name       string
		helperDecl string
		actionBody string
	}{
		{
			name:       "returned_receiver",
			helperDecl: "private Helper helper() { return new Helper(); }",
			actionBody: "helper().danger();",
		},
		{
			name:       "cast_receiver",
			actionBody: "Object value = new Helper();\n    ((Helper)value).danger();",
		},
		{
			name:       "cast_returned_receiver",
			helperDecl: "private Object helper() { return new Helper(); }",
			actionBody: "((Helper)helper()).danger();",
		},
		{
			name:       "parenthesized_receiver",
			actionBody: "Helper helper = new Helper();\n    (helper).danger();",
		},
		{
			name:       "array_receiver",
			actionBody: "Helper[] helpers = new Helper[1];\n    helpers[0].danger();",
		},
		{
			name:       "returned_array_receiver",
			helperDecl: "private Helper[] helpers() { return new Helper[1]; }",
			actionBody: "helpers()[0].danger();",
		},
		{
			name:       "var_receiver",
			actionBody: "var helper = new Helper();\n    helper.danger();",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  ` + tc.helperDecl + `
  public int action() {
    ` + tc.actionBody + `
    return 0;
  }
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  public void danger() {
    lr.decrypt("expression-receiver");
  }
}`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, helper receiver must reach danger()", blockers)
			}
		})
	}
}

func TestConvertJavaResolvesQualifiedReturnedReceiverChains(t *testing.T) {
	tests := []struct {
		name            string
		dangerBody      string
		wantDecrypt     bool
		wantTransaction bool
	}{
		{
			name:        "unsupported_lr_call_blocks_promotion",
			dangerBody:  `lr.decrypt("qualified-returned-receiver");`,
			wantDecrypt: true,
		},
		{
			name: "business_transaction_suppresses_iteration_wrapper",
			dangerBody: `lr.start_transaction("Qualified");
    lr.end_transaction("Qualified", lr.AUTO);`,
			wantTransaction: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `public class Actions {
  private final Factory factory = new Factory();
  public int action() {
    factory.helper().danger();
    return 0;
  }
}`,
				"Factory.java": `public class Factory {
  public Helper helper() { return new Helper(); }
}`,
				"Helper.java": `import lrapi.lr;
public class Helper {
  public void danger() {
    ` + tc.dangerBody + `
  }
}`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(
				javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
				"\n",
			)
			if got := strings.Contains(blockers, "lr_decrypt"); got != tc.wantDecrypt {
				t.Fatalf("runner semantic blockers = %q, lr_decrypt=%t want %t", blockers, got, tc.wantDecrypt)
			}
			emits, _ := result.Script.Actions[0].
				Metadata["emits_business_transactions"].(bool)
			if emits != tc.wantTransaction {
				t.Fatalf(
					"action metadata = %#v, emits transactions=%t want %t",
					result.Script.Actions[0].Metadata,
					emits,
					tc.wantTransaction,
				)
			}
		})
	}
}

func TestJavaQualifiedReturnedReceiverFailsClosedWhenConsumerIsMissing(t *testing.T) {
	program := mustParseJavaTestProgram(t, map[string]string{
		"Actions.java": `public class Actions {
  private Factory factory = new Factory();
  public int action() {
    factory.helper().missing();
    return 0;
  }
}`,
		"Factory.java": `public class Factory {
  public Helper helper() { return new Helper(); }
}`,
		"Helper.java": `public class Helper {
  public void available() {}
}`,
	})
	owner, ok := javaVuserClass(program)
	if !ok {
		t.Fatal("selected Actions class not found")
	}
	action, ok := javaVuserLifecycleMethod(program, "action")
	if !ok || len(action.Statements) == 0 {
		t.Fatal("action lifecycle statement not found")
	}
	index := newJavaEvidenceIndex(program)
	unresolved := javaUnresolvedReturnedReceiverCalls(
		action.Statements[0].Text,
		owner,
		index,
		javaDeclaredVariableTypes(owner, action),
	)
	if len(unresolved) != 1 ||
		!strings.Contains(unresolved[0], "factory.helper().missing") {
		t.Fatalf("unresolved returned receivers = %#v", unresolved)
	}
}

func TestConvertJavaFailsClosedForReachableNestedHelperTypes(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "member_class",
			source: `import lrapi.lr;
public class Actions {
  class Helper {
    void danger() { lr.decrypt("member"); }
  }
  public int action() {
    new Helper().danger();
    return 0;
  }
}`,
		},
		{
			name: "local_class",
			source: `import lrapi.lr;
public class Actions {
  public int action() {
    class Helper {
      void danger() { lr.decrypt("local"); }
    }
    new Helper().danger();
    return 0;
  }
}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ConvertJava(
				map[string]string{"Actions.java": tc.source},
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.ToLower(strings.Join(
				javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
				"\n",
			))
			if !strings.Contains(blockers, "helper") ||
				(!strings.Contains(blockers, "reachability") &&
					!strings.Contains(blockers, "receiver")) {
				t.Fatalf("runner semantic blockers = %q, reachable nested helper must fail closed", blockers)
			}
		})
	}
}

func TestConvertJavaPersistsFunctionalFieldsAcrossLifecycle(t *testing.T) {
	tests := []struct {
		name        string
		initializer string
		constructor string
		initBody    string
		actionBody  string
		endBody     string
		helpers     string
	}{
		{
			name:       "init_to_action",
			initBody:   `task = () -> lr.decrypt("init-to-action");`,
			actionBody: `task.run();`,
		},
		{
			name: "constructor_to_action",
			constructor: `public Actions() { task = () -> lr.decrypt("constructor-to-action"); }
  public Actions(String ignored) { task = null; }`,
			actionBody: `task.run();`,
		},
		{
			name:       "action_to_end",
			actionBody: `task = () -> lr.decrypt("action-to-end");`,
			endBody:    `task.run();`,
		},
		{
			name:        "instance_initializer_to_action",
			initializer: `{ task = () -> lr.decrypt("initializer-to-action"); }`,
			actionBody:  `task.run();`,
		},
		{
			name:       "init_helper_to_action",
			initBody:   `configureTask();`,
			actionBody: `task.run();`,
			helpers: `private void configureTask() {
    task = () -> lr.decrypt("init-helper-to-action");
  }`,
		},
		{
			name:        "constructor_helper_to_action",
			constructor: `public Actions() { configureTask(); }`,
			actionBody:  `task.run();`,
			helpers: `private void configureTask() {
    task = () -> lr.decrypt("constructor-helper-to-action");
  }`,
		},
		{
			name:       "action_helper_to_end",
			actionBody: `this.configureTask();`,
			endBody:    `task.run();`,
			helpers: `private void configureTask() {
    task = () -> lr.decrypt("action-helper-to-end");
  }`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			initMethod := ""
			if tc.initBody != "" {
				initMethod = "public int init() { " + tc.initBody + " return 0; }"
			}
			endMethod := ""
			if tc.endBody != "" {
				endMethod = "public int end() { " + tc.endBody + " return 0; }"
			}
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  private Task task;
  ` + tc.initializer + `
  ` + tc.constructor + `
  ` + initMethod + `
  public int action() {
    ` + tc.actionBody + `
    return 0;
  }
  ` + endMethod + `
  ` + tc.helpers + `
}`,
				"Task.java": `public interface Task {
  void run();
}`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, persistent field callback must retain its body", blockers)
			}
			if strings.Contains(blockers, "functional value task") {
				t.Fatalf("runner semantic blockers = %q, persistent field callback resolved exactly", blockers)
			}
		})
	}
}

func TestConvertJavaFailsClosedForUnassignedCustomSAMField(t *testing.T) {
	tests := []struct {
		name         string
		initBody     string
		helperMethod string
	}{
		{name: "never_assigned"},
		{
			name:     "same_name_local_does_not_persist",
			initBody: `Task task = () -> lr.decrypt("local-only");`,
		},
		{
			name:     "helper_parameter_shadows_field",
			initBody: `configureTask(null);`,
			helperMethod: `private void configureTask(Task task) {
    task = () -> lr.decrypt("helper-parameter-local-only");
  }`,
		},
		{
			name:     "helper_local_shadows_field",
			initBody: `configureTask();`,
			helperMethod: `private void configureTask() {
    Task task = () -> lr.decrypt("helper-local-only");
  }`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			initMethod := ""
			if tc.initBody != "" {
				initMethod = "public int init() { " + tc.initBody + " return 0; }"
			}
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  private Task task;
  ` + initMethod + `
  public int action() {
    task.run();
    return 0;
  }
  ` + tc.helperMethod + `
}`,
				"Task.java": `public interface Task {
  void run();
}`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(javaReviewArtifactBlockers(result, javaRunnerSemanticsKey), "\n")
			if !strings.Contains(blockers, "functional value task") {
				t.Fatalf("runner semantic blockers = %q, unassigned custom SAM must fail closed", blockers)
			}
			if strings.Contains(blockers, "lr_decrypt") {
				t.Fatalf("runner semantic blockers = %q, method-local callback must not persist to field", blockers)
			}
		})
	}
}

func TestConvertJavaAppliesPersistentFunctionalHelpersInStatementOrder(t *testing.T) {
	tests := []struct {
		name            string
		initBody        string
		wantDecrypt     bool
		wantTransaction bool
		wantUnresolved  bool
	}{
		{
			name: "helper_before_direct_assignment",
			initBody: `setTransaction();
    task = () -> lr.decrypt("direct-final");`,
			wantDecrypt: true,
		},
		{
			name: "direct_assignment_before_helper",
			initBody: `task = () -> transaction();
    setReview();`,
			wantDecrypt: true,
		},
		{
			name: "helper_clear_before_direct_assignment",
			initBody: `clearTask();
    task = () -> transaction();`,
			wantTransaction: true,
		},
		{
			name: "direct_assignment_before_helper_clear",
			initBody: `task = () -> transaction();
    clearTask();`,
			wantUnresolved: true,
		},
		{
			name: "mixed_qualified_helpers_follow_source_order",
			initBody: `if (setTransactionFlag() && this.setReviewFlag()) {
    }`,
			wantDecrypt: true,
		},
		{
			name: "mixed_qualified_helpers_reverse_source_order",
			initBody: `if (this.setReviewFlag() && setTransactionFlag()) {
    }`,
			wantTransaction: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  private Task task;
  public int init() {
    ` + tc.initBody + `
    return 0;
  }
  public int action() {
    task.run();
    return 0;
  }
  private void setTransaction() {
    task = () -> transaction();
  }
  private void setReview() {
    task = () -> lr.decrypt("helper-final");
  }
  private void clearTask() {
    task = null;
  }
  private boolean setTransactionFlag() {
    setTransaction();
    return true;
  }
  private boolean setReviewFlag() {
    setReview();
    return true;
  }
  private void transaction() {
    lr.start_transaction("Callback");
    lr.end_transaction("Callback", lr.AUTO);
  }
}`,
				"Task.java": `public interface Task { void run(); }`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(
				javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
				"\n",
			)
			hasDecrypt := strings.Contains(blockers, "lr_decrypt")
			if hasDecrypt != tc.wantDecrypt {
				t.Fatalf(
					"runner semantic blockers = %q, lr_decrypt blocker=%t want %t",
					blockers,
					hasDecrypt,
					tc.wantDecrypt,
				)
			}
			hasUnresolved := strings.Contains(blockers, "functional value task")
			if hasUnresolved != tc.wantUnresolved {
				t.Fatalf(
					"runner semantic blockers = %q, unresolved task=%t want %t",
					blockers,
					hasUnresolved,
					tc.wantUnresolved,
				)
			}
			emits, _ := result.Script.Actions[0].
				Metadata["emits_business_transactions"].(bool)
			if emits != tc.wantTransaction {
				t.Fatalf(
					"action metadata = %#v, emits transactions=%t want %t",
					result.Script.Actions[0].Metadata,
					emits,
					tc.wantTransaction,
				)
			}
		})
	}
}

func TestConvertJavaPreservesPersistentCallbacksAcrossActionIterations(t *testing.T) {
	tests := []struct {
		name             string
		initAssignment   string
		actionAssignment string
		wantDecrypt      bool
		wantTransaction  bool
	}{
		{
			name: "first_iteration_init_callback_remains_reachable",
			initAssignment: `task = () -> {
      lr.start_transaction("FirstIteration");
      lr.end_transaction("FirstIteration", lr.AUTO);
    };`,
			actionAssignment: `task = () -> {};`,
			wantTransaction:  true,
		},
		{
			name:             "later_iteration_callback_remains_reachable",
			initAssignment:   `task = () -> {};`,
			actionAssignment: `task = () -> lr.decrypt("later-iteration");`,
			wantDecrypt:      true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  private Task task;
  public int init() {
    ` + tc.initAssignment + `
    return 0;
  }
  public int action() {
    task.run();
    ` + tc.actionAssignment + `
    return 0;
  }
}`,
				"Task.java": `public interface Task { void run(); }`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(
				javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
				"\n",
			)
			if got := strings.Contains(blockers, "lr_decrypt"); got != tc.wantDecrypt {
				t.Fatalf("runner semantic blockers = %q, lr_decrypt=%t want %t", blockers, got, tc.wantDecrypt)
			}
			if strings.Contains(blockers, "functional value task") {
				t.Fatalf("runner semantic blockers = %q, persistent task should resolve", blockers)
			}
			emits, _ := result.Script.Actions[0].
				Metadata["emits_business_transactions"].(bool)
			if emits != tc.wantTransaction {
				t.Fatalf(
					"action metadata = %#v, emits transactions=%t want %t",
					result.Script.Actions[0].Metadata,
					emits,
					tc.wantTransaction,
				)
			}
		})
	}
}

func TestConvertJavaFollowsThisConstructorDelegationForPersistentFunctionalFields(t *testing.T) {
	tests := []struct {
		name            string
		zeroArgBody     string
		delegatedBody   string
		wantDecrypt     bool
		wantTransaction bool
	}{
		{
			name:          "delegated_constructor_initializes_callback",
			zeroArgBody:   `this("delegated");`,
			delegatedBody: `task = () -> lr.decrypt("delegated");`,
			wantDecrypt:   true,
		},
		{
			name:            "delegating_constructor_overrides_target",
			zeroArgBody:     `this("delegated"); task = () -> transaction();`,
			delegatedBody:   `task = () -> lr.decrypt("overridden");`,
			wantTransaction: true,
		},
		{
			name:          "target_then_delegator_review_override",
			zeroArgBody:   `this("delegated"); task = () -> lr.decrypt("final");`,
			delegatedBody: `task = () -> transaction();`,
			wantDecrypt:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"Actions.java": `import lrapi.lr;
public class Actions {
  private Task task;
  public Actions() { ` + tc.zeroArgBody + ` }
  private Actions(String ignored) { ` + tc.delegatedBody + ` }
  public int action() {
    task.run();
    return 0;
  }
  private void transaction() {
    lr.start_transaction("ConstructorCallback");
    lr.end_transaction("ConstructorCallback", lr.AUTO);
  }
}`,
				"Task.java": `public interface Task { void run(); }`,
			}
			result, err := ConvertJava(
				files,
				ConvertOptions{},
				DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
				nil,
			)
			if err != nil {
				t.Fatalf("ConvertJava: %v", err)
			}
			assertCombinedJavaImport(t, result)
			blockers := strings.Join(
				javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
				"\n",
			)
			hasDecrypt := strings.Contains(blockers, "lr_decrypt")
			if hasDecrypt != tc.wantDecrypt {
				t.Fatalf(
					"runner semantic blockers = %q, lr_decrypt blocker=%t want %t",
					blockers,
					hasDecrypt,
					tc.wantDecrypt,
				)
			}
			emits, _ := result.Script.Actions[0].
				Metadata["emits_business_transactions"].(bool)
			if emits != tc.wantTransaction {
				t.Fatalf(
					"action metadata = %#v, emits transactions=%t want %t",
					result.Script.Actions[0].Metadata,
					emits,
					tc.wantTransaction,
				)
			}
		})
	}
}

func TestConvertJavaFollowsExplicitSuperConstructorForPersistentFunctionalFields(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions extends Base {
  public Actions() { super("configured"); }
  public int action() {
    task.run();
    return 0;
  }
}`,
		"Base.java": `import lrapi.lr;
public class Base {
  protected Task task;
  protected Base(String ignored) {
    task = () -> {
      lr.start_transaction("SuperConstructorCallback");
      lr.end_transaction("SuperConstructorCallback", lr.AUTO);
    };
  }
}`,
		"Task.java": `public interface Task { void run(); }`,
	}
	result, err := ConvertJava(
		files,
		ConvertOptions{},
		DetectedProtocol{CanonicalSourceProtocol: "java_vuser"},
		nil,
	)
	if err != nil {
		t.Fatalf("ConvertJava: %v", err)
	}
	assertCombinedJavaImport(t, result)
	blockers := strings.Join(
		javaReviewArtifactBlockers(result, javaRunnerSemanticsKey),
		"\n",
	)
	if strings.Contains(blockers, "functional value task") {
		t.Fatalf(
			"runner semantic blockers = %q, explicit super constructor must initialize task",
			blockers,
		)
	}
	emits, _ := result.Script.Actions[0].
		Metadata["emits_business_transactions"].(bool)
	if !emits {
		t.Fatalf(
			"action metadata = %#v, super-constructor callback transaction must suppress iteration wrapper",
			result.Script.Actions[0].Metadata,
		)
	}
}
