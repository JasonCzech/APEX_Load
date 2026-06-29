package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
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

func TestConvertZipJavaVuserSidecarFallbackRequiresExplicitOptIn(t *testing.T) {
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

	reviewOnly, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{AllowJavaVuserRunner: true})
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
	callbacks, _ := sidecarAction.Metadata["shim_callbacks"].([]string)
	if len(callbacks) == 0 {
		t.Fatalf("sidecar metadata = %#v, want shim callback list", sidecarAction.Metadata)
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
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 || result.SourceReview.Mappings[0].SupportMode != string(SourceFunctionSupportExternalRequired) {
		t.Fatalf("source review = %#v, want external_required mapping", result.SourceReview)
	}
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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
	if result.Script.Config.JMS.Password != "{{secret.jms_password}}" || result.Script.Config.JMS.Username != "{{secret.jms_username}}" {
		t.Fatalf("jms credentials = %#v, want secret templates", result.Script.Config.JMS)
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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
	if result.Script.Config.JMS.Username != "{{secret.jms_username}}" || result.Script.Config.JMS.TLS == nil || result.Script.Config.JMS.TLS.TrustStoreRef != "gddnsolace.perf.truststore" {
		t.Fatalf("jms config = %#v, want secret username and truststore", result.Script.Config.JMS)
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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
    post.setHeader("Content-Type", "application/json");
    post.setEntity(new StringEntity("{\"id\":\"{orderId}\"}"));
    client.execute(post);
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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
  public int action() throws Throwable {
    Connection conn = DriverManager.getConnection("jdbc:postgresql://db.example.test/orders", "perf_user", "super-secret");
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "database" {
		t.Fatalf("script = %#v, want database", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_jdbc" || result.DetectedProtocol.APEXLoadProtocol != "database" {
		t.Fatalf("detected = %#v, want Java JDBC", result.DetectedProtocol)
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
	assertSourceReviewMapping(t, result.SourceReview.Mappings, "java_sql_connect", "config.database.dsn", "review_only")
	if _, ok := javaActionByType(result.Script.Actions, "db_commit"); !ok {
		t.Fatalf("actions = %#v, want db_commit", result.Script.Actions)
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

func TestConvertZipJavaMixedHTTPAndJDBCSplitsScripts(t *testing.T) {
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
	if len(result.Scripts) < 2 {
		t.Fatalf("scripts = %#v, want split HTTP + database scripts", result.Scripts)
	}
	protocols := map[string]bool{}
	for _, script := range result.Scripts {
		protocols[script.Protocol] = true
	}
	if !protocols["http"] || !protocols["database"] {
		t.Fatalf("protocols = %#v, want http and database", protocols)
	}
}

func TestConvertZipJavaMixedJMSAndJDBCSeparatesCommitOwnership(t *testing.T) {
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
    PreparedStatement ps = conn.prepareStatement("insert into audit values (1)");
    ps.executeUpdate();
    conn.commit();
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
	counts := map[string]int{}
	for _, script := range result.Scripts {
		for _, action := range script.Actions {
			counts[script.Protocol+":"+action.Type]++
		}
	}
	if counts["database:db_commit"] != 1 {
		t.Fatalf("action counts = %#v, want one database db_commit from conn.commit", counts)
	}
	if counts["jms-solace:jms_commit"] != 1 {
		t.Fatalf("action counts = %#v, want one JMS jms_commit from session.commit", counts)
	}
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
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
	if result.Script == nil {
		t.Fatal("script is nil, want review script with unparsed_java_region action")
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeReviewOnly {
		t.Fatalf("detected = %#v, want review-only unparsed Java import", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil || result.ReviewArtifact.Metadata["reason"] != "unparsed_java_region" {
		t.Fatalf("review artifact = %#v, want unparsed_java_region metadata", result.ReviewArtifact)
	}
	assertJavaSourceReviewReason(t, result.effectiveScripts(), "unparsed_java_region")
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 || result.SourceReview.Mappings[0].SupportMode != string(SourceFunctionSupportReviewOnly) {
		t.Fatalf("source review = %#v, want review-only mapping", result.SourceReview)
	}
}

func TestConvertZipJavaMixedHTTPWithMalformedHelperSplitsReviewRegion(t *testing.T) {
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

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Scripts) < 2 {
		t.Fatalf("scripts = %#v, want executable HTTP script plus review script", result.Scripts)
	}
	var sawHTTP bool
	for _, script := range result.Scripts {
		for _, action := range script.Actions {
			if action.Method == "GET" && action.Target == "/home" {
				sawHTTP = true
				break
			}
		}
		if sawHTTP {
			break
		}
	}
	if !sawHTTP {
		t.Fatalf("scripts = %#v, want preserved executable HTTP lane", result.Scripts)
	}
	assertJavaSourceReviewReason(t, result.Scripts, "unparsed_java_region")
	if result.ReviewArtifact == nil || result.ReviewArtifact.Metadata["reason"] != "java_named_review_surfaces" {
		t.Fatalf("review artifact = %#v, want named review surface artifact", result.ReviewArtifact)
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
