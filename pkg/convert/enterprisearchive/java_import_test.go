package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func addJavaZipFile(t *testing.T, zw *zip.Writer, name string, content string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestJavaNativeAnalysisSidecarFallbackRequiresExplicitOptIn(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaCustom/JavaCustom.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaCustom/Actions.java", `public class Actions {
  public int action() throws Throwable {
    CustomClient client = new CustomClient();
    client.invokeBusinessOperation("opaque");
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	reviewOnly, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip without opt-in returned error: %v", err)
	}
	if reviewOnly.Script == nil || reviewOnly.Script.Protocol != "java_vuser" {
		t.Fatalf("review-only default script = %#v, want java_vuser review draft", reviewOnly.Script)
	}
	if _, ok := javaActionByType(reviewOnly.Script.Actions, "java_vuser_run"); ok {
		t.Fatalf("review-only default actions = %#v, did not expect sidecar runner action without opt-in", reviewOnly.Script.Actions)
	}
	if reviewOnly.DetectedProtocol == nil || reviewOnly.DetectedProtocol.ImportMode != ImportModeReviewOnly {
		t.Fatalf("default detected = %#v, want review_only", reviewOnly.DetectedProtocol)
	}
	if issues := dsl.ValidateScriptIssues(reviewOnly.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues(review-only) returned %v", issues)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{AllowJavaVuserRunner: true})
	if err != nil {
		t.Fatalf("ConvertZip with opt-in returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired || result.DetectedProtocol.RunnerRequirement != javaVuserRunnerRequirement {
		t.Fatalf("detected = %#v, want external runner java_vuser", result.DetectedProtocol)
	}
	if result.Script == nil || result.Script.Protocol != "java_vuser" {
		t.Fatalf("script = %#v, want java_vuser sidecar draft", result.Script)
	}
	if result.Script.Config.JavaVuser.Enabled {
		t.Fatal("java_vuser fallback should not enable runner execution by default")
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "java_vuser_run" || result.Script.Actions[0].Target != "Actions" {
		t.Fatalf("actions = %#v, want java_vuser_run Actions", result.Script.Actions)
	}
	sidecarAction := result.Script.Actions[0]
	if sidecarAction.Params["event_contract"] != javaVuserEventContract || sidecarAction.Metadata["event_contract"] != javaVuserEventContract {
		t.Fatalf("sidecar action contract params=%#v metadata=%#v, want %s", sidecarAction.Params, sidecarAction.Metadata, javaVuserEventContract)
	}
	if sidecarAction.Metadata["shim_class"] != javaVuserShimClass || sidecarAction.Metadata["sidecar_transport"] != javaVuserSidecarTransport {
		t.Fatalf("sidecar metadata = %#v, want shim/transport contract", sidecarAction.Metadata)
	}
	requiredRuntimeFeatures, _ := sidecarAction.Metadata["required_runtime_features"].([]string)
	if !stringSliceContains(requiredRuntimeFeatures, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		!stringSliceContains(requiredRuntimeFeatures, protocol.FeatureJavaVuserSessionFrameTokenV1) {
		t.Fatalf("sidecar runtime features = %#v, want persistent lifecycle and authenticated frame-token contracts", requiredRuntimeFeatures)
	}
	callbacks, _ := sidecarAction.Metadata["shim_callbacks"].([]string)
	if len(callbacks) == 0 {
		t.Fatalf("sidecar metadata = %#v, want shim callback list", sidecarAction.Metadata)
	}
	if !stringSliceContains(callbacks, "transaction_set") || !stringSliceContains(callbacks, "external_secret") {
		t.Fatalf("sidecar callbacks = %#v, want full lrapi.lr shim event vocabulary", callbacks)
	}
	paramEvents, _ := sidecarAction.Params["shim_event_types"].([]string)
	if !stringSliceContains(paramEvents, "parameter") || !stringSliceContains(paramEvents, "review") {
		t.Fatalf("sidecar params = %#v, want shim_event_types contract", sidecarAction.Params)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
	if result.ReviewArtifact == nil || result.ReviewArtifact.Protocol.RunnerRequirement != javaVuserRunnerRequirement {
		t.Fatalf("review artifact = %#v, want runner requirement", result.ReviewArtifact)
	}
	if result.ReviewArtifact.Metadata["event_contract"] != javaVuserEventContract || result.ReviewArtifact.Metadata["shim_class"] != javaVuserShimClass {
		t.Fatalf("review artifact metadata = %#v, want sidecar contract", result.ReviewArtifact.Metadata)
	}
	if result.ReviewArtifact.Metadata["sidecar_transport"] != javaVuserSidecarTransport ||
		result.ReviewArtifact.Metadata["lifecycle_mode"] != "persistent_per_vuser" ||
		result.ReviewArtifact.Metadata["required_runtime_feature"] != protocol.FeatureJavaVuserPersistentLifecycleV1 {
		t.Fatalf("review artifact runtime metadata = %#v", result.ReviewArtifact.Metadata)
	}
	reviewRuntimeFeatures, _ := result.ReviewArtifact.Metadata["required_runtime_features"].([]string)
	if !stringSliceContains(reviewRuntimeFeatures, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		!stringSliceContains(reviewRuntimeFeatures, protocol.FeatureJavaVuserSessionFrameTokenV1) {
		t.Fatalf("review artifact runtime features = %#v, want persistent lifecycle and authenticated frame-token contracts", reviewRuntimeFeatures)
	}
	reviewEvents, _ := result.ReviewArtifact.Metadata["shim_event_types"].([]string)
	if !stringSliceContains(reviewEvents, "metric") || !stringSliceContains(reviewEvents, "runtime") {
		t.Fatalf("review artifact metadata = %#v, want full sidecar event contract", result.ReviewArtifact.Metadata)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatalf("source review = %#v, want analyzer mappings", result.SourceReview)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestConvertZipJavaVuserSolaceMapsJMSIdioms(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaSolace/JavaSolace.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
[ManuallyExtraFiles]
FILE1=request.xml
`)
	addJavaZipFile(t, zw, "JavaSolace/request.xml", `<order date="xxxdatexxx">123</order>`)
	addJavaZipFile(t, zw, "JavaSolace/Actions.java", `import lrapi.lr;
import javax.jms.*;
import javax.naming.Context;

public class Actions {
  public int init() throws Throwable {
    env.put(Context.PROVIDER_URL, "smf://broker.example.com:55555");
    env.put("Solace_JMS_VPN", "payments");
    env.put(Context.SECURITY_PRINCIPAL, "loaduser@payments");
    env.put(Context.SECURITY_CREDENTIALS, "super-secret");
    return 0;
  }

  public int action() throws Throwable {
    lr.start_transaction("PublishOrder");
    String body = readFile("request.xml");
    body = body.replaceAll("xxxdatexxx", lr.eval_string("{date}"));
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage(body);
    msg.setStringProperty("source", "lr");
    producer.send(q, msg);
    Message response = consumer.receive(5000);
    session.commit();
    lr.end_transaction("PublishOrder", lr.AUTO);
    return 0;
  }

  public int end() {
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_vuser" || result.DetectedProtocol.ImportMode != ImportModeExecutable {
		t.Fatalf("detected protocol = %#v, want executable java_vuser", result.DetectedProtocol)
	}
	if result.Script == nil || result.Script.Protocol != "jms-solace" {
		t.Fatalf("script = %#v, want jms-solace draft", result.Script)
	}
	if result.Script.Config.JMS.BrokerURL != "smf://broker.example.com:55555" || result.Script.Config.JMS.MessageVPN != "payments" {
		t.Fatalf("jms config = %#v, want broker and vpn mapped", result.Script.Config.JMS)
	}
	if result.Script.Config.JMS.Password != "super-secret" || result.Script.Config.JMS.Username != "loaduser" ||
		result.Script.Config.JMS.Properties["recorded_security_principal"] != "loaduser@payments" {
		t.Fatalf("jms credentials = %#v, want imported values retained", result.Script.Config.JMS)
	}
	var publish *string
	var sawConsume bool
	var sawCommit bool
	for i := range result.Script.Actions {
		action := result.Script.Actions[i]
		switch action.Type {
		case "jms_publish":
			if action.Target != "orders.in" || action.Headers["source"] != "lr" {
				t.Fatalf("publish action = %#v, want queue and source property", action)
			}
			publish = &action.Body
		case "jms_consume":
			sawConsume = true
		case "jms_commit":
			sawCommit = true
		}
	}
	if publish == nil || *publish != `<order date="{{var.date}}">123</order>` {
		t.Fatalf("publish body = %v, want parameterized XML body", publish)
	}
	if !sawConsume || !sawCommit {
		t.Fatalf("actions = %#v, want consume and commit mapped", result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatal("expected Java source review mappings")
	}
}

func TestConvertZipJavaVuserSolaceMapsNestedTryHelperIdioms(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "RealShape/RealShape.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
[ManuallyExtraFiles]
FILE1=GDDN15_Request.xml
`)
	addJavaZipFile(t, zw, "RealShape/GDDN15_Request.xml", `<request date="xxxdatexxx" time="xxxtimexxx"/>`)
	addJavaZipFile(t, zw, "RealShape/gddnsolace.perf.truststore", "placeholder")
	addJavaZipFile(t, zw, "RealShape/Actions.java", `import lrapi.lr;
import javax.jms.*;
import javax.naming.Context;
import javax.naming.InitialContext;
import com.solacesystems.jms.SupportedProperty;

public class Actions {
  Context initialContext;
  Destination destJMS;
  Session session;
  MessageProducer senderJMS;
  String originalXML = null;
  String inputXML = null;

  public int init() throws Throwable {
    // create context
    try {
      createInitialContext();
      connectToServer();
      originalXML = convertXMLFileToString("GDDN15_Request.xml");
    } catch (Exception e) {
      lr.error_message(e.getMessage());
      return -1;
    }
    return 0;
  }

  public int action() throws Throwable {
    lr.start_transaction("PublishRealShape");
    // Main try-catch block
    try {
      lr.save_string(lr.eval_string("MSG_{date}T{time}"), "messageId");
      inputXML = lr.eval_string(originalXML);
      inputXML = inputXML.replaceAll("xxxdatexxx", lr.eval_string("{date}"));
      inputXML = inputXML.replaceAll("xxxtimexxx", lr.eval_string("{time}"));
      // Post request
      try {
        postJMSRequestToQueue();
      } catch (Exception e) {
        lr.error_message(e.getMessage());
        return 1;
      }
    } catch (Exception e) {
      lr.error_message(e.getMessage());
      return 1;
    }
    lr.end_transaction("PublishRealShape", lr.AUTO);
    return 0;
  }

  public int end() {
    return 0;
  }

  public void createInitialContext() throws Exception {
    try {
      env.put(InitialContext.PROVIDER_URL, lr.eval_string("{serverURL}"));
      env.put(Context.SECURITY_PRINCIPAL, lr.eval_string("{userNameJNDI}"));
      env.put(SupportedProperty.SOLACE_JMS_VPN, lr.eval_string("{vpnName}"));
      env.put("Solace_JMS_SSL_TrustStore", "gddnsolace.perf.truststore");
      initialContext = new InitialContext(env);
    } catch (Exception e) {
      throw new Exception("context failed");
    }
  }

  public void connectToServer() throws Exception {
    factory = (ConnectionFactory) initialContext.lookup(lr.eval_string("{connectionFactoryJNDI}"));
    session = connection.createSession(false, Session.AUTO_ACKNOWLEDGE);
    destJMS = (Destination) initialContext.lookup(lr.eval_string("{requestQueueName}"));
  }

  public void postJMSRequestToQueue() throws Exception {
    TextMessage message;
    try {
      senderJMS = session.createProducer(destJMS);
      message = session.createTextMessage(inputXML);
      message.setBooleanProperty(SupportedProperty.SOLACE_JMS_PROP_ISXML, true);
      message.setStringProperty("subscriberNumber", lr.eval_string("{ctn}"));
      message.setStringProperty("seqNumber", lr.eval_string("{messageId}"));
      senderJMS.send(message);
    } catch (Exception e) {
      throw new Exception("post failed");
    } finally {
      senderJMS.close();
    }
  }

  public String convertXMLFileToString(String fileName) throws Exception {
    return "";
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeExecutable {
		t.Fatalf("detected = %#v, want executable Java JMS import", result.DetectedProtocol)
	}
	if result.DetectedProtocol.APEXLoadProtocol != "jms-solace" {
		t.Fatalf("detected = %#v, want jms-solace target", result.DetectedProtocol)
	}
	if result.Script == nil || result.Script.Protocol != "jms-solace" {
		t.Fatalf("script = %#v, want jms-solace draft", result.Script)
	}
	if result.Script.Config.JMS.BrokerURL != "{{var.serverurl}}" || result.Script.Config.JMS.MessageVPN != "{{var.vpnname}}" {
		t.Fatalf("jms config = %#v, want templated broker/vpn", result.Script.Config.JMS)
	}
	if result.Script.Config.JMS.Username != "{{var.usernamejndi}}" || result.Script.Config.JMS.TLS == nil || result.Script.Config.JMS.TLS.TrustStoreRef != "gddnsolace.perf.truststore" {
		t.Fatalf("jms config = %#v, want imported username binding and truststore", result.Script.Config.JMS)
	}

	var publish *dsl.Action
	for i := range result.Script.Actions {
		if result.Script.Actions[i].Type == "jms_publish" {
			publish = &result.Script.Actions[i]
			break
		}
	}
	if publish == nil {
		t.Fatalf("actions = %#v, want jms_publish", result.Script.Actions)
	}
	if publish.Target != "{{var.requestqueuename}}" {
		t.Fatalf("publish target = %q, want requestQueueName template", publish.Target)
	}
	if publish.Body != `<request date="{{var.date}}" time="{{var.time}}"/>` {
		t.Fatalf("publish body = %q, want templated XML", publish.Body)
	}
	if publish.Headers["subscriberNumber"] != "{{var.ctn}}" || publish.Headers["seqNumber"] != "{{var.messageid}}" {
		t.Fatalf("publish headers = %#v, want templated message properties", publish.Headers)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
}

func TestConvertZipJavaWebIdiomsProduceExecutableHTTP(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTP/JavaHTTP.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTP/Actions.java", `import lrapi.lr;

public class Actions {
  public int action() throws Throwable {
    web.addHeader("X-Trace", "abc-123");
    web.url("Home", "URL=https://example.test/app/home", web.LAST);
    web.customRequest("Create", "URL=https://example.test/api/orders", "Method=POST", "Body={\"id\":\"{orderId}\"}", web.LAST);
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want http", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_over_http" || result.DetectedProtocol.APEXLoadProtocol != "http" {
		t.Fatalf("detected = %#v, want Java HTTP", result.DetectedProtocol)
	}
	var home *dsl.Action
	var create *dsl.Action
	for i := range result.Script.Actions {
		switch result.Script.Actions[i].Target {
		case "/app/home":
			home = &result.Script.Actions[i]
		case "/api/orders":
			create = &result.Script.Actions[i]
		}
	}
	if home == nil || home.Method != "GET" || home.Target != "/app/home" {
		t.Fatalf("home action = %#v, want GET /app/home", home)
	}
	if got := home.Headers["X-Trace"]; got != "abc-123" {
		t.Fatalf("home headers = %#v, want X-Trace", home.Headers)
	}
	if len(home.HeaderList) == 0 {
		t.Fatalf("home header_list is empty; headers=%#v", home.Headers)
	}
	if create == nil || create.Method != "POST" || create.Target != "/api/orders" {
		t.Fatalf("create action = %#v, want POST /api/orders", create)
	}
	if create.Body != `{"id":"{{var.orderid}}"}` {
		t.Fatalf("create body = %q, want template-converted body", create.Body)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
}

func TestConvertZipJavaHTTPParsesAnnotatedGenericHelpers(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTPAnnotated/JavaHTTPAnnotated.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTPAnnotated/Actions.java", `public class Actions {
  @Override public int action() throws Throwable {
    helper();
    return 0;
  }

  public <T> T helper() throws Exception {
    web.url("Home", "URL=https://example.test/home", web.LAST);
    return null;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want executable HTTP script", result.Script)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Method != "GET" || result.Script.Actions[0].Target != "/home" {
		t.Fatalf("actions = %#v, want helper web.url converted to /home request", result.Script.Actions)
	}
}

func TestConvertZipJavaHTTPClientBuilderProducesExecutableHTTP(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTPClient/JavaHTTPClient.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTPClient/Actions.java", `import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

public class Actions {
  public int action() throws Throwable {
    HttpClient client = HttpClient.newHttpClient();
    HttpRequest request = HttpRequest.newBuilder()
        .uri(URI.create("https://example.test/api/orders"))
        .header("Content-Type", "application/json")
        .POST(HttpRequest.BodyPublishers.ofString("{\"id\":\"{orderId}\"}"))
        .build();
    HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want executable HTTP script", result.Script)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one HTTP request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" || action.Target != "/api/orders" {
		t.Fatalf("action = %#v, want POST /api/orders", action)
	}
	if action.Body != `{"id":"{{var.orderid}}"}` {
		t.Fatalf("body = %q, want templated JSON body", action.Body)
	}
	if action.Headers["Content-Type"] != "application/json" || len(action.HeaderList) == 0 {
		t.Fatalf("headers=%#v header_list=%#v, want ordered content type", action.Headers, action.HeaderList)
	}
}

func TestConvertZipJavaHTTPClientSplitBuilderProducesExecutableHTTP(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTPSplitBuilder/JavaHTTPSplitBuilder.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTPSplitBuilder/Actions.java", `import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

public class Actions {
  public int action() throws Throwable {
    HttpClient client = HttpClient.newHttpClient();
    HttpRequest.Builder builder = HttpRequest.newBuilder();
    builder.uri(URI.create("https://example.test/api/orders"));
    builder.header("Content-Type", "application/json");
    builder.POST(HttpRequest.BodyPublishers.ofString("{\"id\":\"{orderId}\"}"));
    HttpRequest request = builder.build();
    HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want executable HTTP script", result.Script)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one HTTP request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" || action.Target != "/api/orders" {
		t.Fatalf("action = %#v, want POST /api/orders", action)
	}
	if action.Body != `{"id":"{{var.orderid}}"}` {
		t.Fatalf("body = %q, want templated JSON body", action.Body)
	}
	if action.Headers["Content-Type"] != "application/json" || len(action.HeaderList) == 0 {
		t.Fatalf("headers=%#v header_list=%#v, want ordered content type", action.Headers, action.HeaderList)
	}
}

func TestConvertZipJavaHTTPPreservesLRParameterSetup(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTPParams/JavaHTTPParams.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTPParams/Actions.java", `import lrapi.lr;

public class Actions {
  public int action() throws Throwable {
    lr.save_string(lr.eval_string("{UserId}"), "Copy");
    web.url("Profile", "URL=https://example.test/users/{Copy}", web.LAST);
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want executable HTTP script", result.Script)
	}
	if _, ok := javaActionByType(result.Script.Actions, "source_parameter"); !ok {
		t.Fatalf("actions = %#v, want lr.save_string preserved as source_parameter", result.Script.Actions)
	}
	request, ok := javaActionByType(result.Script.Actions, "GET")
	if !ok || request.Target != "/users/{{var.copy}}" {
		t.Fatalf("request = %#v ok=%v, want GET /users/{{var.copy}}", request, ok)
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "lr_save_string", "actions[0]", "converted")
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "web_url", "actions[1]", "converted")
}

func TestConvertZipJavaHTTPUnknownLRForcesReviewArtifact(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTPUnknownLR/JavaHTTPUnknownLR.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTPUnknownLR/Actions.java", `import lrapi.lr;

public class Actions {
  public int action() throws Throwable {
    lr.frobnicate("x");
    web.url("Home", "URL=https://example.test/home", web.LAST);
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want primary executable HTTP script", result.Script)
	}
	if len(result.effectiveScripts()) < 2 || result.ReviewArtifact == nil {
		t.Fatalf("result = %#v, want HTTP script plus review artifact for unknown lr.*", result)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "lr_frobnicate", SourceFunctionSupportReviewOnly)
	for _, script := range result.effectiveScripts() {
		for _, action := range script.Actions {
			if action.Type == "custom_call" {
				t.Fatalf("unknown Java lr.* emitted custom_call: %#v", action)
			}
		}
	}
}

func TestConvertZipJavaApacheHTTPClientObjectProducesExecutableHTTP(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaApacheHTTP/JavaApacheHTTP.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaApacheHTTP/Actions.java", `import org.apache.http.client.methods.HttpPost;
import org.apache.http.entity.StringEntity;
import org.apache.http.impl.client.CloseableHttpClient;
import org.apache.http.impl.client.HttpClients;

public class Actions {
  public int action() throws Throwable {
    CloseableHttpClient client = HttpClients.createDefault();
    HttpPost post = new HttpPost("https://example.test/api/orders");
    post.setHeader("Content-Type", "text/plain");
    post.setHeader("Content-Type", "application/json");
    post.setEntity(new StringEntity("{\"id\":\"{orderId}\"}"));
    client.execute(post);
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want executable HTTP script", result.Script)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one HTTP request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" || action.Target != "/api/orders" {
		t.Fatalf("action = %#v, want POST /api/orders", action)
	}
	if action.Body != `{"id":"{{var.orderid}}"}` {
		t.Fatalf("body = %q, want templated JSON body", action.Body)
	}
	if action.Headers["Content-Type"] != "application/json" || len(action.HeaderList) == 0 {
		t.Fatalf("headers=%#v header_list=%#v, want ordered content type", action.Headers, action.HeaderList)
	}
	if got := javaHeaderListValues(action.HeaderList, "Content-Type"); len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("Content-Type header_list values = %#v, want one replacement value", got)
	}
}

func TestConvertZipJavaHttpURLConnectionObjectFlowProducesExecutableHTTP(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaURLConnection/JavaURLConnection.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaURLConnection/Actions.java", `import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;

public class Actions {
  public int action() throws Throwable {
    URL url = new URL("https://example.test/api/orders");
    HttpURLConnection conn = (HttpURLConnection) url.openConnection();
    conn.setRequestMethod("POST");
    conn.setRequestProperty("Content-Type", "text/plain");
    conn.setRequestProperty("Content-Type", "application/json");
    conn.addRequestProperty("Accept", "application/json");
    conn.addRequestProperty("Accept", "text/plain");
    conn.setDoOutput(true);
    OutputStream os = conn.getOutputStream();
    os.write("{\"id\":\"{orderId}\"}".getBytes(StandardCharsets.UTF_8));
    int code = conn.getResponseCode();
    return code;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want executable HTTP script", result.Script)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one HTTP request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" || action.Target != "/api/orders" {
		t.Fatalf("action = %#v, want POST /api/orders", action)
	}
	if action.Body != `{"id":"{{var.orderid}}"}` {
		t.Fatalf("body = %q, want templated JSON body", action.Body)
	}
	if action.Headers["Content-Type"] != "application/json" || len(action.HeaderList) == 0 {
		t.Fatalf("headers=%#v header_list=%#v, want ordered content type", action.Headers, action.HeaderList)
	}
	if got := javaHeaderListValues(action.HeaderList, "Content-Type"); len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("Content-Type header_list values = %#v, want one replacement value", got)
	}
	wantHeaders := dsl.HeaderList{
		{Name: "Content-Type", Value: "application/json"},
		{Name: "Accept", Value: "application/json"},
		{Name: "Accept", Value: "text/plain"},
	}
	if len(action.HeaderList) < len(wantHeaders) {
		t.Fatalf("header_list=%#v, want at least %#v", action.HeaderList, wantHeaders)
	}
	for i, want := range wantHeaders {
		if action.HeaderList[i] != want {
			t.Fatalf("header_list[%d]=%#v, want %#v in ordered duplicate header list %#v", i, action.HeaderList[i], want, action.HeaderList)
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
}

func TestConvertZipJavaJDBCIdiomsProduceExecutableDatabase(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaDB/JavaDB.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaDB/Actions.java", `import java.sql.*;

public class Actions {
  private static final String JDBC_URL = "jdbc:postgresql://db.example.test/orders";
  private final String JDBC_USER = "perf_user";
  private static final String JDBC_PASSWORD = "super-secret";

  public int action() throws Throwable {
    String password = "temporary-password";
    password = Actions.JDBC_PASSWORD;
    Connection conn = DriverManager.getConnection(JDBC_URL, this.JDBC_USER, password);
    String sql = "select id, status from orders where id = ?";
    PreparedStatement ps = conn.prepareStatement(sql);
    ps.executeQuery();
    conn.commit();
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "database" {
		t.Fatalf("script = %#v, want database", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_jdbc" || result.DetectedProtocol.APEXLoadProtocol != "database" {
		t.Fatalf("detected = %#v, want Java JDBC", result.DetectedProtocol)
	}
	if result.Script.Config.Database.Driver != "postgres" {
		t.Fatalf("database driver = %q, want postgres", result.Script.Config.Database.Driver)
	}
	dsn, err := url.Parse(result.Script.Config.Database.DSN)
	if err != nil {
		t.Fatalf("parse retained database DSN: %v", err)
	}
	if dsn.User == nil {
		t.Fatalf("database DSN = %q, want retained JDBC credentials", result.Script.Config.Database.DSN)
	}
	password, hasPassword := dsn.User.Password()
	if dsn.Scheme != "postgres" ||
		dsn.Host != "db.example.test" ||
		dsn.Path != "/orders" ||
		dsn.User.Username() != "perf_user" ||
		!hasPassword ||
		password != "super-secret" {
		t.Fatalf("database DSN = %q, want retained PostgreSQL URL/user/password semantics", result.Script.Config.Database.DSN)
	}
	var query *dsl.Action
	for i := range result.Script.Actions {
		if result.Script.Actions[i].Type == "db_query" {
			query = &result.Script.Actions[i]
			break
		}
	}
	if query == nil || query.Target != "select id, status from orders where id = ?" {
		t.Fatalf("query action = %#v, want db_query SQL", query)
	}
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "java_sql_connect", "config.database.dsn", "converted")
	reviewPayload, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatalf("marshal source review: %v", err)
	}
	for _, expected := range []string{"super-secret", "temporary-password"} {
		if !strings.Contains(string(reviewPayload), expected) {
			t.Fatalf("source review lost JDBC password material %q: %s", expected, reviewPayload)
		}
	}
	if _, ok := javaActionByType(result.Script.Actions, "db_commit"); !ok {
		t.Fatalf("actions = %#v, want db_commit", result.Script.Actions)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(strings.ToLower(warning), "replace it") ||
			strings.Contains(strings.ToLower(warning), "re-enter") {
			t.Fatalf("warning requires operator credential reentry: %q", warning)
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
}

func TestConvertZipJavaJDBCDynamicSQLProducesNamedReview(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaDynamicDB/JavaDynamicDB.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaDynamicDB/Actions.java", `import java.sql.*;

public class Actions {
  public int action() throws Throwable {
    Connection conn = DriverManager.getConnection("jdbc:postgresql://db.example.test/orders", "perf_user", "super-secret");
    String sql = "select id, status from " + tableName;
    PreparedStatement ps = conn.prepareStatement(sql);
    ps.executeQuery();
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "database" {
		t.Fatalf("script = %#v, want database review script", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeReviewOnly {
		t.Fatalf("detected = %#v, want review-only dynamic SQL import", result.DetectedProtocol)
	}
	assertSourceReviewFunctionStatus(t, result.SourceReview.Mappings, "java_sql_dynamic", "review_only")
	var reviewCount int
	for _, action := range result.Script.Actions {
		if action.Type == "source_review" && action.Metadata["review_reason"] == "unresolved_dynamic_sql" {
			reviewCount++
		}
	}
	if reviewCount == 0 {
		t.Fatalf("actions = %#v, want unresolved_dynamic_sql source_review action", result.Script.Actions)
	}
	if _, ok := javaActionByType(result.Script.Actions, "db_query"); ok {
		t.Fatalf("actions = %#v, dynamic SQL must not produce executable db_query", result.Script.Actions)
	}
}

func TestConvertZipJavaMixedHTTPAndJDBCKeepsCombinedScript(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaMixed/JavaMixed.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaMixed/Actions.java", `import lrapi.lr;
import java.sql.*;

public class Actions {
  public int action() throws Throwable {
    web.url("Home", "URL=https://example.test/home", web.LAST);
    PreparedStatement ps = conn.prepareStatement("select 1");
    ps.executeQuery();
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Scripts) != 1 || result.Script != result.Scripts[0] {
		t.Fatalf("scripts = %#v, want one combined script", result.Scripts)
	}
	if result.Script.Protocol != "java_vuser" || result.DetectedProtocol == nil || result.DetectedProtocol.APEXLoadProtocol != "java_vuser" || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired {
		t.Fatalf("result = script %#v detected %#v, want combined external java_vuser", result.Script, result.DetectedProtocol)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "java_vuser_run" || result.Script.Actions[0].Params["method"] != "action" {
		t.Fatalf("actions = %#v, want one Java action phase", result.Script.Actions)
	}
}

func TestConvertZipJavaMixedJMSAndJDBCKeepsCommitOwnershipInJava(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaMixedTx/JavaMixedTx.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaMixedTx/Actions.java", `import javax.jms.*;
import java.sql.*;

public class Actions {
  public int action() throws Throwable {
    PreparedStatement ps = con.prepareStatement("insert into audit values (1)");
    ps.executeUpdate();
    con.commit();
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage("hi");
    producer.send(q, msg);
    session.commit();
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Scripts) != 1 || result.Script != result.Scripts[0] || result.Script.Protocol != "java_vuser" {
		t.Fatalf("scripts = %#v, want one combined java_vuser", result.Scripts)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "java_vuser_run" {
		t.Fatalf("actions = %#v, want one Java action so JDBC/JMS commit ownership remains in source", result.Script.Actions)
	}
}

func javaHeaderListValues(headers dsl.HeaderList, name string) []string {
	var values []string
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			values = append(values, header.Value)
		}
	}
	return values
}

func TestConvertZipJavaSynchronizedBlockPreservesNestedJMS(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaSync/JavaSync.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaSync/Actions.java", `import javax.jms.*;

public class Actions {
  public int action() throws Throwable {
    synchronized (this) {
      Queue q = session.createQueue("orders.in");
      TextMessage msg = session.createTextMessage("hi");
      producer.send(q, msg);
    }
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "jms-solace" {
		t.Fatalf("script = %#v, want JMS script", result.Script)
	}
	publish, ok := javaActionByType(result.Script.Actions, "jms_publish")
	if !ok {
		t.Fatalf("actions = %#v, want nested jms_publish", result.Script.Actions)
	}
	if publish.Target != "orders.in" || publish.Body != "hi" {
		t.Fatalf("publish = %#v, want nested publish target/body", publish)
	}
}

func TestConvertZipJavaJMSInfersProviderAndTopicDestination(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaMQ/JavaMQ.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaMQ/Actions.java", `import javax.jms.*;
import com.ibm.mq.jms.MQTopicConnectionFactory;

public class Actions {
  public int action() throws Throwable {
    Topic topic = session.createTopic("prices.topic");
    TextMessage msg = session.createTextMessage("price={price}");
    producer.send(topic, msg);
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "jms-ibmmq" {
		t.Fatalf("script = %#v, want jms-ibmmq", result.Script)
	}
	if result.Script.Config.JMS.Provider != "ibmmq" || result.Script.Config.JMS.DestinationType != "topic" {
		t.Fatalf("jms config = %#v, want IBM MQ topic", result.Script.Config.JMS)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.APEXLoadProtocol != "jms-ibmmq" {
		t.Fatalf("detected = %#v, want jms-ibmmq target", result.DetectedProtocol)
	}
	publish, ok := javaActionByType(result.Script.Actions, "jms_publish")
	if !ok {
		t.Fatalf("actions = %#v, want jms_publish", result.Script.Actions)
	}
	if publish.Target != "prices.topic" || publish.Body != "price={{var.price}}" {
		t.Fatalf("publish action = %#v, want topic target and templated body", publish)
	}
}

func TestConvertZipJavaProtocolsDoesNotClobberExplicitIBMMQProvider(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaProtocolsMQ/JavaProtocolsMQ.usr", `[General]
Type=Java_protocols
ActiveTypes=Java_protocols
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaProtocolsMQ/Actions.java", `import javax.jms.*;
import com.ibm.mq.jms.MQQueueConnectionFactory;

public class Actions {
  public int action() throws Throwable {
    Queue queue = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage("hi");
    producer.send(queue, msg);
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "jms-ibmmq" {
		t.Fatalf("script = %#v, want Java_protocols IBM MQ script", result.Script)
	}
	if result.Script.Config.JMS.Provider != "ibmmq" || result.Script.Config.JMS.DestinationType != "queue" {
		t.Fatalf("jms config = %#v, want IBM MQ queue despite Java_protocols metadata", result.Script.Config.JMS)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.APEXLoadProtocol != "jms-ibmmq" {
		t.Fatalf("detected = %#v, want IBM MQ protocol target", result.DetectedProtocol)
	}
}

func TestConvertZipJavaSOAPWithRecoverableEnvelopeProducesHTTP(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaSOAP/JavaSOAP.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaSOAP/Actions.java", `import javax.xml.soap.*;

public class Actions {
  public int action() throws Throwable {
    String endpoint = "https://example.test/services/order";
    String soapEnvelope = "<soapenv:Envelope><soapenv:Body><order>{orderId}</order></soapenv:Body></soapenv:Envelope>";
    SOAPMessage message = null;
    SOAPConnection connection = SOAPConnectionFactory.newInstance().createConnection();
    connection.call(message, endpoint);
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "soap" {
		t.Fatalf("script = %#v, want native SOAP transport", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_soap" {
		t.Fatalf("detected = %#v, want java_soap", result.DetectedProtocol)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one SOAP HTTP request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Method != "POST" || action.Target != "/services/order" {
		t.Fatalf("action = %#v, want POST /services/order SOAP request", action)
	}
	if action.Body != "<soapenv:Envelope><soapenv:Body><order>{{var.orderid}}</order></soapenv:Body></soapenv:Envelope>" {
		t.Fatalf("body = %q, want templated SOAP envelope", action.Body)
	}
	if action.Headers["Content-Type"] != "text/xml" || len(action.HeaderList) == 0 {
		t.Fatalf("headers = %#v header_list=%#v, want text/xml with header_list", action.Headers, action.HeaderList)
	}
}

func TestConvertZipJavaNoRuntimeSurfacesUseNamedReviewDispositions(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaReview/JavaReview.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaReview/Actions.java", `import javax.ejb.EJBHome;
import org.omg.CORBA.ORB;
import javax.xml.ws.Service;

public class Actions {
  public int action() throws Throwable {
    javax.ejb.EJBHome home = null;
    org.omg.CORBA.ORB orb = org.omg.CORBA.ORB.init(new String[0], null);
    Object remote = javax.naming.Naming.lookup("rmi://example.test/service");
    javax.xml.soap.SOAPMessage msg = null;
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeReviewOnly {
		t.Fatalf("detected = %#v, want review-only named dispositions", result.DetectedProtocol)
	}
	reasons := map[string]bool{}
	for _, action := range result.Script.Actions {
		if action.Type != "source_review" || action.Metadata == nil {
			continue
		}
		if reason, ok := action.Metadata["review_reason"].(string); ok {
			reasons[reason] = true
		}
	}
	for _, reason := range []string{"ejb_review", "corba_review", "rmi_java_review", "soap_java_review"} {
		if !reasons[reason] {
			t.Fatalf("review reasons = %#v, missing %s", reasons, reason)
		}
	}
}

func TestConvertZipJavaUnmatchedBraceProducesUnparsedRegionReview(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaBroken/JavaBroken.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaBroken/Actions.java", `public class Actions {
  public int action() throws Throwable {
    web.url("Home", "URL=https://example.test/home", web.LAST);
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "java_vuser" || len(result.Scripts) != 1 {
		t.Fatalf("script = %#v scripts=%#v, want singleton combined Java draft", result.Script, result.Scripts)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired {
		t.Fatalf("detected = %#v, want external-runner unparsed Java import", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil || result.ReviewArtifact.Metadata["reason"] != javaVuserReviewReason {
		t.Fatalf("review artifact = %#v, want combined Java runner review", result.ReviewArtifact)
	}
	// The unparsed region was never analyzed, so its preserved mapping must be
	// review_only rather than external_required: counting it as converted let
	// the fidelity report claim full conversion for unreadable source.
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 || result.SourceReview.Mappings[0].SupportMode != string(SourceFunctionSupportReviewOnly) || result.SourceReview.Mappings[0].Status != "review_only" {
		t.Fatalf("source review = %#v, want preserved review-only mapping for the unparsed region", result.SourceReview)
	}
}

func TestConvertZipJavaMixedHTTPWithMalformedHelperFailsClosed(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaMixedBroken/JavaMixedBroken.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaMixedBroken/Actions.java", `public class Actions {
  public int action() throws Throwable {
    web.url("Home", "URL=https://example.test/home", web.LAST);
    return 0;
  }

  public void brokenHelper() throws Exception {
    client.call();
`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	_, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err == nil || !strings.Contains(err.Error(), "no parseable Java lifecycle methods") {
		t.Fatalf("ConvertZip error = %v, want malformed outer class to fail closed", err)
	}
}

func assertJavaSourceReviewReason(t *testing.T, scripts []*dsl.Script, reason string) {
	t.Helper()
	for _, script := range scripts {
		if script == nil {
			continue
		}
		for _, action := range script.Actions {
			if action.Type != "source_review" || action.Metadata == nil {
				continue
			}
			if action.Metadata["review_reason"] == reason {
				return
			}
		}
	}
	t.Fatalf("scripts = %#v, missing source_review reason %q", scripts, reason)
}

// F39: a header set to different values in mutually-exclusive if/else branches must
// be emitted once (only one branch runs at replay), not appended twice into a
// request that carries a duplicated header.
func TestConvertZipJavaHTTPClientConditionalHeaderEmittedOnce(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTPCondHeader/JavaHTTPCondHeader.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTPCondHeader/Actions.java", `import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

public class Actions {
  public int action() throws Throwable {
    boolean prod = System.getenv("ENV") != null;
    HttpClient client = HttpClient.newHttpClient();
    HttpRequest.Builder builder = HttpRequest.newBuilder();
    builder.uri(URI.create("https://example.test/api/orders"));
    if (prod) {
      builder.header("Authorization", "Bearer prod-token");
    } else {
      builder.header("Authorization", "Bearer test-token");
    }
    builder.GET();
    HttpRequest request = builder.build();
    HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || len(result.Script.Actions) != 1 {
		t.Fatalf("script = %#v, want one HTTP request", result.Script)
	}
	action := result.Script.Actions[0]
	got := javaHeaderListValues(action.HeaderList, "Authorization")
	if len(got) != 1 {
		t.Fatalf("Authorization header_list values = %#v, want exactly one (branches are mutually exclusive)", got)
	}
	if got[0] != "Bearer prod-token" {
		t.Fatalf("Authorization value = %q, want imported executable header bytes preserved", got[0])
	}
	assertEnterpriseCredentialProvenancePreserved(
		t,
		result,
		[]string{"prod-token", "test-token"},
	)
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
}

// F39 guard: two INDEPENDENT sequential if blocks (not an if/else pair) that both
// set the same header can both run at replay, so java.net.http append semantics
// must be preserved — both values are emitted, not collapsed.
func TestConvertZipJavaHTTPClientIndependentIfHeadersBothKept(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTPTwoIf/JavaHTTPTwoIf.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTPTwoIf/Actions.java", `import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

public class Actions {
  public int action() throws Throwable {
    boolean a = System.getenv("A") != null;
    boolean b = System.getenv("B") != null;
    HttpClient client = HttpClient.newHttpClient();
    HttpRequest.Builder builder = HttpRequest.newBuilder();
    builder.uri(URI.create("https://example.test/api/orders"));
    if (a) {
      builder.header("X-Tag", "one");
    }
    if (b) {
      builder.header("X-Tag", "two");
    }
    builder.GET();
    HttpRequest request = builder.build();
    HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || len(result.Script.Actions) != 1 {
		t.Fatalf("script = %#v, want one HTTP request", result.Script)
	}
	got := javaHeaderListValues(result.Script.Actions[0].HeaderList, "X-Tag")
	if len(got) != 2 {
		t.Fatalf("X-Tag values = %#v, want both (independent ifs can both run)", got)
	}
}

// F39 (builder-var reuse): a SECOND request built by reassigning the same builder
// variable must still collapse a header set in its own if/else branches — the
// dedup state must not carry the first request's stale branch tracking.
func TestConvertZipJavaHTTPClientReusedBuilderConditionalHeaderEmittedOnce(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaHTTPReuse/JavaHTTPReuse.usr", `[General]
Type=Java over HTTP
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaHTTPReuse/Actions.java", `import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;

public class Actions {
  public int action() throws Throwable {
    boolean prod = System.getenv("ENV") != null;
    HttpClient client = HttpClient.newHttpClient();
    HttpRequest.Builder builder = HttpRequest.newBuilder();
    builder.uri(URI.create("https://example.test/api/first"));
    if (prod) {
      builder.header("Authorization", "Bearer first-prod");
    } else {
      builder.header("Authorization", "Bearer first-test");
    }
    builder.GET();
    HttpRequest first = builder.build();
    client.send(first, HttpResponse.BodyHandlers.ofString());
    builder = HttpRequest.newBuilder();
    builder.uri(URI.create("https://example.test/api/second"));
    if (prod) {
      builder.header("Authorization", "Bearer second-prod");
    } else {
      builder.header("Authorization", "Bearer second-test");
    }
    builder.GET();
    HttpRequest second = builder.build();
    client.send(second, HttpResponse.BodyHandlers.ofString());
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	result, err := convertZipJavaNativeAnalysisForTest(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want two HTTP requests", result.Script)
	}
	for _, action := range result.Script.Actions {
		got := javaHeaderListValues(action.HeaderList, "Authorization")
		if len(got) != 1 {
			t.Fatalf("action %s Authorization values = %#v, want exactly one", action.Target, got)
		}
	}
}

// The live import must pay for one evidence index, not one per pass. The passes
// are checked by handing them an index built from a DIFFERENT program: a pass
// that still rebuilds its own index from its program argument cannot see the
// extra class, so its disposition gives the reuse away.
func TestJavaImportPassesConsumeTheSharedEvidenceIndex(t *testing.T) {
	runner := javaClass{
		Name:       "Actions",
		File:       "Actions.java",
		Kind:       "class",
		Public:     true,
		SuperClass: "Base",
		Source:     "public int action() { return 0; }",
	}
	program := javaProgram{Classes: []javaClass{runner}}
	withBase := javaProgram{Classes: []javaClass{
		runner,
		{Name: "Base", File: "Base.java", Kind: "class", Public: true},
	}}

	if blockers := javaVuserInheritanceBlockers(program, nil); len(blockers) != 1 ||
		!strings.Contains(blockers[0], "unresolved superclass") {
		t.Fatalf("inheritance blockers without a shared index = %#v, want the unresolved-superclass blocker", blockers)
	}
	shared := newJavaEvidenceIndex(withBase)
	if blockers := javaVuserInheritanceBlockers(program, shared); len(blockers) != 0 {
		t.Fatalf("inheritance blockers with a shared index = %#v, want the shared index to resolve Base", blockers)
	}
	if sources := javaVuserRunnerSourceFiles(program, runner, nil, shared); !sources["base.java"] {
		t.Fatalf("runner sources with a shared index = %#v, want Base.java from the shared index", sources)
	}
	if reachability := buildJavaVuserRuntimeReachabilityWithEvidenceIndex(program, shared); reachability.evidenceIndex != shared {
		t.Fatalf("reachability evidence index = %p, want the shared index %p", reachability.evidenceIndex, shared)
	}
}

// Sharing one index must not shrink the governed per-pass lookup budgets: each
// pass starts from the same allowance a freshly built index would give it.
func TestJavaSharedEvidenceIndexRebasesPerPassBudgets(t *testing.T) {
	program := javaProgram{Classes: []javaClass{
		{Name: "Actions", File: "Actions.java", Kind: "class", Public: true, Source: "public int action() { return 0; }"},
	}}
	index := newJavaEvidenceIndex(program)
	index.totalMemberLookups = maxJavaTotalMemberLookups
	index.staticImportLookups = maxJavaStaticImportOwnerLookups
	index.staticImportLimitHit = true

	shared := javaSharedEvidenceIndexForPass(index, program)
	if shared != index {
		t.Fatalf("shared index = %p, want the caller's index %p", shared, index)
	}
	if shared.totalMemberLookups != 0 || shared.staticImportLookups != 0 || shared.staticImportLimitHit {
		t.Fatalf(
			"rebased budgets = (%d, %d, %t), want a fresh allowance",
			shared.totalMemberLookups,
			shared.staticImportLookups,
			shared.staticImportLimitHit,
		)
	}
	if rebuilt := javaSharedEvidenceIndexForPass(nil, program); rebuilt == nil {
		t.Fatal("javaSharedEvidenceIndexForPass(nil) = nil, want a freshly built index")
	}
}

// Static-import owner lookups are charged only on a cache miss, so a shared
// index must hand the next pass a cold staticMethodCache. Otherwise the second
// pass charges nothing, the governed limit can no longer be reached, and the
// fail-closed "static-import helper analysis exceeded" blocker silently stops
// being raised for archives a freshly built index would have stopped.
func TestJavaSharedEvidenceIndexRechargesStaticImportLookups(t *testing.T) {
	owner := javaClass{
		Name:   "Actions",
		File:   "Actions.java",
		Kind:   "class",
		Public: true,
		Source: "public int action() { return 0; }",
		Imports: []string{
			"static com.example.Helpers.*",
			"static com.example.MoreHelpers.*",
			"static com.example.ExactHelpers.helper",
		},
	}
	program := javaProgram{Classes: []javaClass{owner}}

	index := newJavaEvidenceIndex(program)
	javaEvidenceStaticImportedMethods(index, owner, "helper")
	firstPass := index.staticImportLookups
	if firstPass == 0 {
		t.Fatal("first pass static-import lookups = 0, want the imported owners to be charged")
	}

	shared := javaSharedEvidenceIndexForPass(index, program)
	javaEvidenceStaticImportedMethods(shared, owner, "helper")
	if shared.staticImportLookups != firstPass {
		t.Fatalf(
			"second pass static-import lookups = %d, want %d (the charge a freshly built index makes)",
			shared.staticImportLookups,
			firstPass,
		)
	}
}

// The member-lookup budget is charged per hierarchy level walked, but a warm
// fieldLookupCache answers in one. A shared index must therefore start each pass
// with a cold field cache so the receiver and total member budgets are consumed
// at the same rate a freshly built index consumes them.
func TestJavaSharedEvidenceIndexRechargesMemberLookups(t *testing.T) {
	base := javaClass{
		Name:   "Base",
		File:   "Base.java",
		Kind:   "class",
		Public: true,
		Fields: []javaStatement{{Text: "private Connection conn;"}},
	}
	runner := javaClass{
		Name:       "Actions",
		File:       "Actions.java",
		Kind:       "class",
		Public:     true,
		SuperClass: "Base",
		Source:     "public int action() { return 0; }",
	}
	program := javaProgram{Classes: []javaClass{runner, base}}

	index := newJavaEvidenceIndex(program)
	work := 0
	if _, found, exceeded := index.fieldTypeInHierarchy(runner, "conn", &work); !found || exceeded {
		t.Fatalf("first pass field lookup = (found %t, exceeded %t), want the inherited field", found, exceeded)
	}
	firstPass := index.totalMemberLookups
	if firstPass < 2 {
		t.Fatalf("first pass member lookups = %d, want the walk through Base to be charged", firstPass)
	}

	shared := javaSharedEvidenceIndexForPass(index, program)
	work = 0
	if _, found, exceeded := shared.fieldTypeInHierarchy(runner, "conn", &work); !found || exceeded {
		t.Fatalf("second pass field lookup = (found %t, exceeded %t), want the inherited field", found, exceeded)
	}
	if shared.totalMemberLookups != firstPass {
		t.Fatalf(
			"second pass member lookups = %d, want %d (the charge a freshly built index makes)",
			shared.totalMemberLookups,
			firstPass,
		)
	}
}

// A live import threads one index through every pass, so any state a pass leaves
// behind must be cleared before the next pass starts: only the structural maps a
// fresh index builds from the program may survive.
func TestJavaSharedEvidenceIndexClearsWarmPassCaches(t *testing.T) {
	files := map[string]string{
		"Actions.java": "import static com.example.Helpers.*;\n" +
			"public class Actions {\n" +
			"    private java.sql.Connection conn;\n" +
			"    public int init() { return 0; }\n" +
			"    public int action() { lr.start_transaction(\"t\"); lr.end_transaction(\"t\", lr.AUTO); return 0; }\n" +
			"}\n",
	}
	program, _ := parseJavaProgram(files)
	index := newJavaEvidenceIndex(program)
	buildJavaVuserRuntimeReachabilityWithEvidenceIndex(program, index)

	shared := javaSharedEvidenceIndexForPass(index, program)
	warm := map[string]int{
		"fieldLookupCache":     len(shared.fieldLookupCache),
		"staticMethodCache":    len(shared.staticMethodCache),
		"bindingCache":         len(shared.bindingCache),
		"importScopes":         len(shared.importScopes),
		"subtypeCache":         len(shared.subtypeCache),
		"hierarchyFieldTypes":  len(shared.hierarchyFieldTypes),
		"functionalBindings":   len(shared.functionalBindings),
		"persistentFunctional": len(shared.persistentFunctional),
	}
	for name, size := range warm {
		if size != 0 {
			t.Errorf("%s after rebase = %d entries, want the cold cache a fresh index starts with", name, size)
		}
	}
	if shared.functionalBindingBuilds != 0 {
		t.Errorf("functional timeline builds after rebase = %d, want 0", shared.functionalBindingBuilds)
	}
	// The structural maps are pure program data and must still be shared, or the
	// pass would pay for the rescan this reuse exists to remove.
	if len(shared.qualified) == 0 || len(shared.classIndices) == 0 {
		t.Fatalf(
			"structural maps after rebase = (%d qualified, %d class indices), want the shared index to survive",
			len(shared.qualified),
			len(shared.classIndices),
		)
	}
}
