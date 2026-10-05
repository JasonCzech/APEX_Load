package jmeter

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestResolveGroupProtocol(t *testing.T) {
	tests := []struct {
		name    string
		counts  map[string]int
		want    string
		wantHet bool
	}{
		{"empty defaults http", map[string]int{}, "http", false},
		{"homogeneous database", map[string]int{"database": 3}, "database", false},
		{"homogeneous network", map[string]int{"network": 2}, "network", false},
		{"homogeneous jms", map[string]int{"jms": 1}, "jms", false},
		{"mixed prefers http", map[string]int{"http": 1, "database": 2}, "http", true},
		{"mixed db over network", map[string]int{"database": 1, "network": 1}, "database", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, het := resolveGroupProtocol(tc.counts)
			if got != tc.want || het != tc.wantHet {
				t.Fatalf("resolveGroupProtocol(%v) = (%q,%v), want (%q,%v)", tc.counts, got, het, tc.want, tc.wantHet)
			}
		})
	}
}

func TestDetectDatabaseDriverAndDSN(t *testing.T) {
	tests := []struct {
		url        string
		wantDriver string
		wantDSN    string
		wantOK     bool
	}{
		{"jdbc:postgresql://db:5432/orders", "postgres", "postgres://app:pw@db:5432/orders", true},
		{"jdbc:mysql://db:3306/shop", "mysql", "app:pw@tcp(db:3306)/shop", true},
		{"jdbc:sqlite:/tmp/test.db", "sqlite", "/tmp/test.db", true},
		{"jdbc:redis://nope", "", "", false},
	}
	for _, tc := range tests {
		driver, dsn, _, ok := detectDatabaseDriverAndDSN(tc.url, "app", "pw")
		if driver != tc.wantDriver || ok != tc.wantOK || (tc.wantOK && dsn != tc.wantDSN) {
			t.Fatalf("detect(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.url, driver, dsn, ok, tc.wantDriver, tc.wantDSN, tc.wantOK)
		}
		if tc.wantOK && tc.wantDriver != "sqlite" && !strings.Contains(dsn, "pw") {
			t.Fatalf("detect(%q) did not retain the imported password in DSN %q", tc.url, dsn)
		}
	}
}

func TestDetectDatabaseDSNOmitsCredentialsWhenNoPassword(t *testing.T) {
	// No password supplied: the username may appear but no secret placeholder is
	// injected, and nothing cleartext leaks.
	driver, dsn, _, ok := detectDatabaseDriverAndDSN("jdbc:postgresql://db:5432/orders", "app", "")
	if !ok || driver != "postgres" {
		t.Fatalf("detect = (%q,%q,%v), want postgres ok", driver, dsn, ok)
	}
	if strings.Contains(dsn, "{{secret") {
		t.Fatalf("dsn = %q, want no secret placeholder when no password present", dsn)
	}
}

func TestDetectDatabaseDSNRetainsInlineURLCredentials(t *testing.T) {
	// Credentials embedded directly in the JDBC URL (no separate password
	// property) remain usable in the durable DSN, and the inline-credential flag
	// is set so the importer adds a review item.
	tests := []struct {
		name    string
		url     string
		secret  string
		wantDrv string
	}{
		{"oracle thin user/pass@", "jdbc:oracle:thin:scott/tiger@//orahost:1521/svc", "tiger", "oracle"},
		{"oracle SID user/pass@", "jdbc:oracle:thin:scott/tiger@orahost:1521:orcl", "tiger", "oracle"},
		{"sqlserver ;password=", "jdbc:sqlserver://mssql:1433;databaseName=app;user=sa;password=Sup3rSecret", "Sup3rSecret", "odbc"},
		{"jtds ;password=", "jdbc:jtds:sqlserver://mssql:1433/app;user=sa;password=Sup3rSecret", "Sup3rSecret", "odbc"},
		{"mysql userinfo", "jdbc:mysql://root:hunter2@db:3306/shop", "hunter2", "mysql"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			driver, dsn, inlineCred, ok := detectDatabaseDriverAndDSN(tc.url, "", "")
			if !ok || driver != tc.wantDrv {
				t.Fatalf("detect(%q) = (%q,_,%v), want driver %q ok", tc.url, driver, ok, tc.wantDrv)
			}
			if !inlineCred {
				t.Fatalf("detect(%q): inlineCred=false, want true (inline credential present)", tc.url)
			}
			if !strings.Contains(dsn, tc.secret) {
				t.Fatalf("detect(%q) did not retain credential %q in DSN %q", tc.url, tc.secret, dsn)
			}
		})
	}
}

func TestHasJDBCInlineCredentials(t *testing.T) {
	for _, url := range []string{
		"oracle:thin:@//orahost:1521/svc",
		"sqlserver://mssql:1433;databaseName=app",
		"customdb://host/db",
	} {
		if hasJDBCInlineCredentials(url) {
			t.Fatalf("hasJDBCInlineCredentials(%q)=true, want false", url)
		}
	}
}

func TestParseJMeterJDBCConnectionPropertiesUsesDBCPPrecedence(t *testing.T) {
	properties := parseJMeterJDBCConnectionProperties(
		"sslmode=disable;flag;sslmode=require;password=first;password=last;",
	)
	want := []jmeterJDBCConnectionProperty{
		{Name: "sslmode", Value: "require"},
		{Name: "flag", Value: ""},
		{Name: "password", Value: "last"},
	}
	if len(properties) != len(want) {
		t.Fatalf("properties = %#v, want %#v", properties, want)
	}
	for index := range want {
		if properties[index] != want[index] {
			t.Fatalf("properties[%d] = %#v, want %#v", index, properties[index], want[index])
		}
	}
}

func TestApplyJMeterJDBCConnectionPropertiesCredentialPrecedence(t *testing.T) {
	properties := parseJMeterJDBCConnectionProperties(
		"sslmode=require;user=property-user;password=property-password;sslpassword=ssl-secret",
	)

	fromProperties, applied := applyJMeterJDBCConnectionProperties(
		"postgres",
		"postgres://url-user:url-password@db.example.test/orders?sslmode=disable",
		properties,
		false,
	)
	if !applied {
		t.Fatal("PostgreSQL connection properties were not applied")
	}
	parsed, err := url.Parse(fromProperties)
	if err != nil {
		t.Fatalf("parse property DSN: %v", err)
	}
	if parsed.User == nil || parsed.User.Username() != "property-user" {
		t.Fatalf("property DSN user = %#v, want property-user", parsed.User)
	}
	if password, ok := parsed.User.Password(); !ok || password != "property-password" {
		t.Fatalf("property DSN password = %q, %v, want property-password", password, ok)
	}
	if parsed.Query().Get("sslmode") != "require" ||
		parsed.Query().Get("sslpassword") != "ssl-secret" {
		t.Fatalf("property DSN query = %#v", parsed.Query())
	}

	fromExplicit, applied := applyJMeterJDBCConnectionProperties(
		"postgres",
		"postgres://explicit-user:explicit-password@db.example.test/orders?sslmode=disable",
		properties,
		true,
	)
	if !applied {
		t.Fatal("PostgreSQL non-auth connection properties were not applied")
	}
	parsed, err = url.Parse(fromExplicit)
	if err != nil {
		t.Fatalf("parse explicit DSN: %v", err)
	}
	if parsed.User == nil || parsed.User.Username() != "explicit-user" {
		t.Fatalf("explicit DSN user = %#v, want explicit-user", parsed.User)
	}
	if password, ok := parsed.User.Password(); !ok || password != "explicit-password" {
		t.Fatalf("explicit DSN password = %q, %v, want explicit-password", password, ok)
	}
	if strings.Contains(fromExplicit, "property-user") ||
		strings.Contains(fromExplicit, "property-password") {
		t.Fatalf("connectionProperties auth overrode dedicated fields: %q", fromExplicit)
	}
	if parsed.Query().Get("sslmode") != "require" ||
		parsed.Query().Get("sslpassword") != "ssl-secret" {
		t.Fatalf("explicit DSN query = %#v", parsed.Query())
	}
}

func TestConvertJMXJDBCConnectionPropertiesAreAppliedAndPreserved(t *testing.T) {
	const (
		connectionProperties = "sslmode=require;application_name=orders;user=property-user;password=property-password;sslpassword=ssl-secret"
		explicitPassword     = "explicit-password"
	)
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="DB Properties" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="DB Users" enabled="true"/>
      <hashTree>
        <JDBCDataSource testname="Orders Pool" enabled="true">
          <stringProp name="dataSource">pool1</stringProp>
          <stringProp name="dbUrl">jdbc:postgresql://db.example.test:5432/orders?sslmode=disable&amp;application_name=old</stringProp>
          <stringProp name="driver">org.postgresql.Driver</stringProp>
          <stringProp name="username">explicit-user</stringProp>
          <stringProp name="password">` + explicitPassword + `</stringProp>
          <stringProp name="connectionProperties">` + connectionProperties + `</stringProp>
        </JDBCDataSource>
        <hashTree/>
        <JDBCSampler testname="Select Orders" enabled="true">
          <stringProp name="dataSource">pool1</stringProp>
          <stringProp name="query">SELECT 1</stringProp>
          <stringProp name="queryType">Select Statement</stringProp>
        </JDBCSampler>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "properties.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	script := result.Scripts[0]
	parsed, err := url.Parse(script.Config.Database.DSN)
	if err != nil {
		t.Fatalf("parse imported DSN: %v", err)
	}
	if parsed.User == nil || parsed.User.Username() != "explicit-user" {
		t.Fatalf("imported DSN user = %#v, want explicit-user", parsed.User)
	}
	if password, ok := parsed.User.Password(); !ok || password != explicitPassword {
		t.Fatalf("imported DSN password = %q, %v, want %q", password, ok, explicitPassword)
	}
	if got := parsed.Query().Get("sslmode"); got != "require" {
		t.Fatalf("sslmode = %q, want require", got)
	}
	if got := parsed.Query().Get("application_name"); got != "orders" {
		t.Fatalf("application_name = %q, want orders", got)
	}
	if got := parsed.Query().Get("sslpassword"); got != "ssl-secret" {
		t.Fatalf("sslpassword = %q, want retained credential", got)
	}
	if strings.Contains(script.Config.Database.DSN, "property-password") {
		t.Fatalf("property password overrode dedicated password: %q", script.Config.Database.DSN)
	}
	sources, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedJMeterJDBCSourcesKey].([]map[string]string)
	if len(sources) != 1 ||
		sources[0]["connection_properties"] != connectionProperties ||
		sources[0]["password"] != explicitPassword {
		t.Fatalf("preserved JDBC source = %#v", sources)
	}
	sourceReview, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatalf("marshal source review: %v", err)
	}
	for _, expected := range []string{
		explicitPassword,
		"property-password",
		"ssl-secret",
	} {
		if !strings.Contains(string(sourceReview), expected) {
			t.Fatalf("source review lost JDBC credential %q: %s", expected, sourceReview)
		}
	}
	if result.SourceReview == nil ||
		len(result.SourceReview.SourceFiles) != 1 ||
		result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("source review = %#v, want exact JDBC source", result.SourceReview)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("generated database script failed validation: %#v", issues)
	}
}

func TestConvertJMXJDBCThreadGroupBecomesExecutableDatabaseScript(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="DB Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="DB Users" enabled="true"/>
      <hashTree>
        <JDBCDataSource testname="Orders Pool" enabled="true">
          <stringProp name="dataSource">pool1</stringProp>
          <stringProp name="dbUrl">jdbc:postgresql://db.example.com:5432/orders</stringProp>
          <stringProp name="driver">org.postgresql.Driver</stringProp>
          <stringProp name="username">app</stringProp>
          <stringProp name="password">secret</stringProp>
        </JDBCDataSource>
        <hashTree/>
        <JDBCSampler testname="Select Orders" enabled="true">
          <stringProp name="dataSource">pool1</stringProp>
          <stringProp name="query">SELECT * FROM orders WHERE id = ${orderId}</stringProp>
          <stringProp name="queryType">Select Statement</stringProp>
        </JDBCSampler>
        <hashTree/>
        <JDBCSampler testname="Mark Shipped" enabled="true">
          <stringProp name="query">UPDATE orders SET status='shipped' WHERE id = 1</stringProp>
          <stringProp name="queryType">Update Statement</stringProp>
        </JDBCSampler>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "db.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	script := result.Scripts[0]
	if script.Protocol != "database" {
		t.Fatalf("protocol = %q, want database", script.Protocol)
	}
	if len(script.Actions) != 2 || script.Actions[0].Type != "db_query" || script.Actions[1].Type != "db_exec" {
		t.Fatalf("actions = %#v, want db_query + db_exec", script.Actions)
	}
	if !strings.Contains(script.Actions[0].Body, "{{var.orderId}}") {
		t.Fatalf("query body = %q, want JMeter variable converted", script.Actions[0].Body)
	}
	if script.Config.Database.Driver != "postgres" {
		t.Fatalf("database driver = %q, want postgres", script.Config.Database.Driver)
	}
	if !strings.Contains(script.Config.Database.DSN, "db.example.com:5432/orders") {
		t.Fatalf("database dsn = %q, want host+db", script.Config.Database.DSN)
	}
	if !strings.Contains(script.Config.Database.DSN, "app:secret@") {
		t.Fatalf("database dsn = %q, want imported password retained for replay", script.Config.Database.DSN)
	}
	sources, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedJMeterJDBCSourcesKey].([]map[string]string)
	if len(sources) != 1 || sources[0]["password"] != "secret" {
		t.Fatalf("preserved JDBC sources = %#v, want exact imported password", sources)
	}
	if result.Stats.AdapterRequired < 1 {
		t.Fatalf("adapter_required stat = %d, want >= 1 (connection)", result.Stats.AdapterRequired)
	}
	// The generated database script must be executable (pass DSL validation).
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("generated database script failed validation: %#v", issues)
	}
	// Accounting (convert-other-2): the two converted JDBC samplers must be counted
	// as converted, not silently reported as skipped with zero converted actions.
	if result.Stats.ConvertedActions != 2 {
		t.Fatalf("ConvertedActions = %d, want 2 (both JDBC samplers converted)", result.Stats.ConvertedActions)
	}
	if result.Stats.SkippedSamplers != 0 {
		t.Fatalf("SkippedSamplers = %d, want 0 (converted samplers must not be reported skipped)", result.Stats.SkippedSamplers)
	}
	if gap := result.Stats.TotalSamplers - result.Stats.ConvertedActions - result.Stats.SkippedSamplers; gap != 0 {
		t.Fatalf("sampler accounting gap = %d, want 0 (total=%d converted=%d skipped=%d)",
			gap, result.Stats.TotalSamplers, result.Stats.ConvertedActions, result.Stats.SkippedSamplers)
	}
}

func TestConvertJMXNetworkSamplersBecomeExecutableNetworkScripts(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Net Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="TCP Users" enabled="true"/>
      <hashTree>
        <TCPSampler testname="Send Ping" enabled="true">
          <stringProp name="TCPSampler.server">tcp.example.com</stringProp>
          <stringProp name="TCPSampler.port">9000</stringProp>
          <stringProp name="TCPSampler.request">PING</stringProp>
        </TCPSampler>
        <hashTree/>
      </hashTree>
      <ThreadGroup testname="FTP Users" enabled="true"/>
      <hashTree>
        <FTPSampler testname="Fetch File" enabled="true">
          <stringProp name="FTPSampler.server">ftp.example.com</stringProp>
          <stringProp name="FTPSampler.port">21</stringProp>
          <stringProp name="FTPSampler.filename">/pub/data.csv</stringProp>
          <boolProp name="FTPSampler.uploadfile">false</boolProp>
        </FTPSampler>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "net.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2 (tcp + ftp)", len(result.Scripts))
	}
	var tcp, ftp *dsl.Script
	for _, s := range result.Scripts {
		if len(s.Actions) == 1 {
			switch s.Actions[0].Type {
			case "tcp_request":
				tcp = s
			case "ftp_command":
				ftp = s
			}
		}
	}
	if tcp == nil || tcp.Protocol != "network" {
		t.Fatalf("tcp script not found among %#v", result.Scripts)
	}
	if tcp.Actions[0].Target != "tcp.example.com:9000" || tcp.Config.Network.Subprotocol != "tcp" {
		t.Fatalf("tcp action target/subprotocol = %q / %q", tcp.Actions[0].Target, tcp.Config.Network.Subprotocol)
	}
	if ftp == nil || ftp.Protocol != "network" {
		t.Fatalf("ftp script not found among %#v", result.Scripts)
	}
	if ftp.Actions[0].Target != "RETR /pub/data.csv" || ftp.Config.Network.Subprotocol != "ftp" {
		t.Fatalf("ftp action target/subprotocol = %q / %q", ftp.Actions[0].Target, ftp.Config.Network.Subprotocol)
	}
	for _, s := range result.Scripts {
		if issues := dsl.ValidateScriptIssues(s); len(issues) != 0 {
			t.Fatalf("generated network script %q failed validation: %#v", s.Name, issues)
		}
	}
}

func TestConvertJMXJMSSamplerIsAdapterRequired(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="JMS Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="JMS Users" enabled="true"/>
      <hashTree>
        <JMSPublisher testname="Publish Order" enabled="true">
          <stringProp name="jms.topic">orders.topic</stringProp>
          <stringProp name="jms.text_message">hello</stringProp>
        </JMSPublisher>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "jms.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	// A pure-JMS group is adapter_required: no runnable script is produced.
	if len(result.Scripts) != 0 {
		t.Fatalf("scripts = %d, want 0 for adapter_required JMS", len(result.Scripts))
	}
	if result.Stats.AdapterRequired < 1 {
		t.Fatalf("adapter_required stat = %d, want >= 1", result.Stats.AdapterRequired)
	}
	if !containsJMeterDiagnostic(result, "JMeter JMSPublisher") {
		t.Fatalf("diagnostics = %#v, want JMSPublisher adapter note", result.Unsupported)
	}
}

func TestConvertJMXHeterogeneousGroupReviewGatesNonDominant(t *testing.T) {
	const jmx = `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.6.3">
  <hashTree>
    <TestPlan testname="Mixed Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="Mixed Users" enabled="true"/>
      <hashTree>
        <HTTPSamplerProxy testname="Home" enabled="true">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
        <JDBCSampler testname="Audit" enabled="true">
          <stringProp name="query">INSERT INTO audit VALUES (1)</stringProp>
          <stringProp name="queryType">Update Statement</stringProp>
        </JDBCSampler>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "mixed.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	script := result.Scripts[0]
	if script.Protocol != "http" {
		t.Fatalf("protocol = %q, want http (dominant)", script.Protocol)
	}
	if len(script.Actions) != 1 || script.Actions[0].Type != "GET" {
		t.Fatalf("actions = %#v, want only the HTTP GET", script.Actions)
	}
	if result.Stats.ReviewRequired < 1 {
		t.Fatalf("review_required stat = %d, want >= 1 (review-gated JDBC)", result.Stats.ReviewRequired)
	}
	var sawMixedWarning bool
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "mixed-protocol") {
			sawMixedWarning = true
		}
	}
	if !sawMixedWarning {
		t.Fatalf("warnings = %#v, want a mixed-protocol warning", result.Warnings)
	}
}

// A LoadRunner Enterprise JMeter plan interleaved Event Hubs samplers with HTTP
// samplers in one thread group. The Event Hubs samplers became actions in the
// HTTP script, failed its validation and took the whole import down.
func TestConvertJMXHeterogeneousGroupReviewGatesMessagingSamplers(t *testing.T) {
	const jmx = "\ufeff" + `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2" properties="5.0" jmeter="5.4.1">
  <hashTree>
    <TestPlan testname="Mixed Plan" enabled="true"/>
    <hashTree>
      <ThreadGroup testname="API" enabled="true"/>
      <hashTree>
        <GenericController testname="email" enabled="true"/>
        <hashTree>
          <HTTPSamplerProxy testname="Notify" enabled="true">
            <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
            <stringProp name="HTTPSampler.protocol">https</stringProp>
            <stringProp name="HTTPSampler.path">/notify</stringProp>
            <stringProp name="HTTPSampler.method">GET</stringProp>
          </HTTPSamplerProxy>
          <hashTree/>
        </hashTree>
        <GenericController testname="SMS" enabled="true"/>
        <hashTree>
          <jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler testclass="jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler" testname="hub west" enabled="true">
            <stringProp name="namespaceName">west.servicebus.windows.net</stringProp>
            <stringProp name="eventHubName">hub</stringProp>
          </jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler>
          <hashTree/>
        </hashTree>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "mixed.jmx"})
	if err != nil {
		t.Fatalf("ConvertJMX returned error: %v", err)
	}
	if len(result.Scripts) != 1 || result.Scripts[0].Protocol != "http" {
		t.Fatalf("scripts = %#v, want one HTTP script", result.Scripts)
	}
	if actions := result.Scripts[0].Actions; len(actions) != 1 || actions[0].Type != "GET" {
		t.Fatalf("actions = %#v, want only the HTTP GET", actions)
	}
	if !containsJMeterDiagnostic(result, "JMeter "+pnopEventHubsSampler) {
		t.Fatalf("diagnostics = %#v, want the review-gated Event Hubs sampler", result.Diagnostics)
	}
	gated := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == pnopEventHubsSampler && mapping.Status == "review_only" {
			gated = true
		}
	}
	if !gated {
		t.Fatal("Event Hubs sampler has no review_only source mapping")
	}
}

// A TestAction pause before a review-gated messaging sampler is consumed by it,
// as for a review-only JMS sampler, instead of delaying the next HTTP request.
func TestGatedMessagingSamplerConsumesPrecedingPause(t *testing.T) {
	const jmx = `<?xml version="1.0"?><jmeterTestPlan><hashTree>
<TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree>
<HTTPSamplerProxy testname="Before" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/before</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
<TestAction testname="Pause" enabled="true"><stringProp name="ActionProcessor.duration">250</stringProp></TestAction><hashTree/>
<jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler testname="hub" enabled="true"><stringProp name="eventHubName">hub</stringProp></jp.co.pnop.jmeter.protocol.azureeventhubs.sampler.AzEventHubsSampler><hashTree/>
<HTTPSamplerProxy testname="After" enabled="true"><stringProp name="HTTPSampler.domain">api.example</stringProp><stringProp name="HTTPSampler.path">/after</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(jmx), ConvertOptions{FileName: "pause.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Scripts[0].Actions
	if len(actions) != 2 || actions[1].Target != "/after" {
		t.Fatalf("actions = %#v", actions)
	}
	if actions[1].ThinkTime.MeanMs != 0 {
		t.Fatalf("pause leaked past the gated messaging sampler: %#v", actions[1].ThinkTime)
	}
}
