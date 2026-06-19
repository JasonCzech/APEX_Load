package jmeter

import (
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
		driver, dsn, ok := detectDatabaseDriverAndDSN(tc.url, "app", "pw")
		if driver != tc.wantDriver || ok != tc.wantOK || (tc.wantOK && dsn != tc.wantDSN) {
			t.Fatalf("detect(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.url, driver, dsn, ok, tc.wantDriver, tc.wantDSN, tc.wantOK)
		}
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
	if result.Stats.AdapterRequired < 1 {
		t.Fatalf("adapter_required stat = %d, want >= 1 (connection)", result.Stats.AdapterRequired)
	}
	// The generated database script must be executable (pass DSL validation).
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("generated database script failed validation: %#v", issues)
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
