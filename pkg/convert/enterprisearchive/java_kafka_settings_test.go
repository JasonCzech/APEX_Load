package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Synthetic equivalent of a Java producer archive. No customer payload,
// credentials, broker address, workstation path, or source files are copied.
const javaKafkaProducerSettingsFixture = `import lrapi.lr;
import java.util.*;
import org.apache.kafka.clients.producer.*;
public class Actions {
  public int init() { return 0; }
  public int action() {
    String topicName = "example.events";
    String value = "prefix-" + lr.eval_string("{trace_id}");
    Properties props = new Properties();
    props.put("bootstrap.servers", "broker.example.invalid:9093");
    props.put("key.serializer", "org.apache.kafka.common.serialization.StringSerializer");
    props.put("value.serializer", "org.apache.kafka.common.serialization.StringSerializer");
    props.put("sasl.jaas.config", "org.apache.kafka.common.security.plain.PlainLoginModule required username='fixture-user' password='fixture-secret-canary';");
    props.put("group", "fixture-group");
    props.put("security.protocol", "SASL_SSL");
    props.put("sasl.mechanism", "PLAIN");
    props.put("max.block.ms", "30000");
    props.put("acks", "all");
    props.put("retries", "1");
    props.put("batch.size", "16384");
    props.put("linger.ms", "1");
    props.put("session.timeout.ms", "4500");
    props.put("heartbeat.interval.ms", "3000");
    props.put("buffer.memory", "33554432");
    props.put("enable.idempotence", "false");
    props.put("ssl.enabled.protocols", "TLSv1.2");
    Producer<String,String> producer = new KafkaProducer<String,String>(props);
    ProducerRecord<String,String> record = new ProducerRecord<String,String>(topicName,"1",value);
    producer.send(record);
    producer.close();
    return 0;
  }
  public int end() { return 0; }
}`

func TestConvertZipJavaKafkaSettingsInventoryPreservesGovernedDraft(t *testing.T) {
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "KafkaFixture", map[string]string{"Actions.java": javaKafkaProducerSettingsFixture}), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script.Protocol != "java_vuser" || len(result.Scripts) != 1 || result.Script.Config.JavaVuser.Enabled {
		t.Fatal("Kafka evidence changed the governed Java execution lane")
	}
	if result.Script.Config.Kafka.Producer != nil || len(result.Script.Config.Kafka.Brokers) != 0 {
		t.Fatal("migration evidence was accidentally applied as executable Kafka config")
	}
	report, ok := result.ReviewArtifact.Metadata[JavaKafkaSettingsMetadataKey].(*JavaKafkaSettingsReport)
	if !ok || report == nil {
		t.Fatal("missing Kafka settings inventory")
	}
	if len(report.Settings) != 17 || report.StatusCounts["supported"] != 12 || report.StatusCounts["requires_binding"] != 1 || report.StatusCounts["partial"] != 1 || report.StatusCounts["non_applicable"] != 3 {
		t.Fatalf("property inventory: count=%d statuses=%v", len(report.Settings), report.StatusCounts)
	}
	if report.NativeMigrationReady || len(report.NativeMigrationBlockers) < 3 {
		t.Fatal("static property evidence incorrectly establishes native replay fidelity")
	}
	if result.Script.Metadata.Import.Preserved[JavaKafkaSettingsMetadataKey] != report {
		t.Fatal("completed import omitted Kafka review evidence")
	}
	bytes, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-secret-canary", "fixture-user"} {
		if strings.Contains(string(bytes), secret) {
			t.Fatal("Kafka settings evidence disclosed a JAAS credential")
		}
	}
	for _, setting := range report.Settings {
		if setting.SourceFile != "Actions.java" || setting.StartLine < 1 || setting.EndLine < setting.StartLine {
			t.Fatalf("property %s lacks source location", setting.Name)
		}
		mapped := false
		for _, mapping := range result.SourceReview.Mappings {
			if mapping.SourceFile == setting.SourceFile && mapping.StartLine == setting.StartLine && strings.Contains(mapping.Notes, "Kafka property "+setting.Name+":") {
				mapped = true
			}
		}
		if !mapped {
			t.Fatalf("source review lacks Kafka property %s", setting.Name)
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("draft validation: %v", issues)
	}
}

func TestJavaKafkaSettingClassification(t *testing.T) {
	for _, test := range []struct {
		name, value, status string
	}{
		{"retries", "-1", "unsupported"},
		{"bootstrap.servers", "", "unsupported"},
		{"bootstrap.servers", "broker.example.invalid:9093,[::1]:9092", "supported"},
		{"bootstrap.servers", "broker.example.invalid:99999", "unsupported"},
		{"batch.size", "garbage-secret-canary", "unsupported"},
		{"batch.size", "0", "partial"},
		{"retries", "0", "supported"},
		{"max.block.ms", "0", "supported"},
		{"max.block.ms", "99999999999999999999999", "unsupported"},
		{"linger.ms", "3600001", "unsupported"},
		{"enable.idempotence", "false", "supported"},
		{"enable.idempotence", "unknown", "unsupported"},
		{"acks", "1", "supported"},
		{"acks", "arbitrary", "unsupported"},
		{"key.serializer", "com.example.CustomSerializer", "unsupported"},
		{"ssl.enabled.protocols", "TLSv1.2,TLSv1.3", "supported"},
		{"ssl.enabled.protocols", "TLSv1.1", "unsupported"},
		{"sasl.mechanism", "GSSAPI", "unsupported"},
		{"custom.property", "unknown-value-secret-canary", "unsupported"},
	} {
		t.Run(test.name+"="+test.value, func(t *testing.T) {
			setting := classifyJavaKafkaSetting(test.name, test.value, true)
			if setting.GoSupport != test.status {
				t.Fatalf("status=%s want %s", setting.GoSupport, test.status)
			}
			if setting.GoSupport == "unsupported" && setting.Value != "[value withheld]" {
				t.Fatal("unverified property value was disclosed")
			}
		})
	}
}

func TestJavaKafkaInventoryKeepsDynamicConditionalAndDuplicateRows(t *testing.T) {
	program, _ := parseJavaProgram(map[string]string{"Actions.java": `import org.apache.kafka.clients.producer.*;
public class Actions {
 public int action() {
   Properties props = new Properties();
   Properties unrelated = new Properties();
   unrelated.put("password", "unrelated-secret-canary");
   props.put("acks", "all");
   props.put("acks", "1");
   props.put("security.protocol", System.getenv("PROTOCOL"));
   if (true) { props.put("retries", "2"); }
   props.setProperty("private.custom", "private-secret-canary");
   KafkaProducer producer = new KafkaProducer(props);
   return 0;
 }
}`})
	report := buildJavaKafkaSettingsReport(program)
	if report == nil || len(report.Settings) != 5 || report.StatusCounts["unresolved"] != 2 || report.StatusCounts["unsupported"] != 1 {
		t.Fatalf("unexpected inventory: %#v", report)
	}
	if report.Settings[0].Name != "acks" || report.Settings[1].Name != "acks" || report.Settings[0].Value != "all" || report.Settings[1].Value != "1" {
		t.Fatal("duplicate source assignments were silently collapsed")
	}
	serialized, _ := json.Marshal(report)
	if strings.Contains(string(serialized), "secret-canary") {
		t.Fatal("unknown or unrelated property escaped into review evidence")
	}
}

func TestJavaKafkaInventoryBoundAndNonKafkaSource(t *testing.T) {
	var source strings.Builder
	source.WriteString("public class Actions { public int action() { Properties props = new Properties();\n")
	for i := 0; i < maxJavaKafkaSettingEvidence+1; i++ {
		fmt.Fprintf(&source, "props.put(\"property.%d\", \"value\");\n", i)
	}
	source.WriteString("KafkaProducer producer = new KafkaProducer(props); return 0; } }")
	program, _ := parseJavaProgram(map[string]string{"Actions.java": source.String()})
	report := buildJavaKafkaSettingsReport(program)
	if report == nil || len(report.Settings) != maxJavaKafkaSettingEvidence || !report.SettingsTruncated || report.NativeMigrationReady {
		t.Fatal("bounded Kafka review did not report incomplete inventory")
	}
	program, _ = parseJavaProgram(map[string]string{"Actions.java": "public class Actions { public int action() { Properties props = new Properties(); props.put(\"password\", \"secret\"); return 0; } }"})
	if report := buildJavaKafkaSettingsReport(program); report != nil {
		t.Fatal("non-Kafka source acquired Kafka review metadata")
	}
	program, _ = parseJavaProgram(map[string]string{"Actions.java": "public class Actions { public int action() { /* KafkaProducer producer = new KafkaProducer(props); */ Properties props = new Properties(); props.put(\"acks\", \"all\"); return 0; } }"})
	if report := buildJavaKafkaSettingsReport(program); report != nil {
		t.Fatal("commented constructor was treated as Kafka source")
	}
}
