package brokerconfig

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"mqsc qlocal", "DEFINE QLOCAL('ORDER.Q') MAXDEPTH(5000)", FormatMQSC},
		{"mqsc lowercase", "define topic(EVENTS) topicstr('events/#')", FormatMQSC},
		{"jndi queue", "queue.OrderQueue = ORDER.QUEUE", FormatJNDI},
		{"jndi connectionfactory", "connectionFactory.cf = amqp://broker", FormatJNDI},
		// Regression: a JNDI value that contains "DEFINE TOPIC" must NOT be read as MQSC.
		{"jndi value mentions define topic", "queue.MyQueue=APP.QUEUE\ntopic.Notices=DEFINE TOPIC THING", FormatJNDI},
		// Regression: an MQSC file whose only DEFINE is on a comment line is still MQSC via the live DEFINE.
		{"mqsc with comment", "* DEFINE QLOCAL('OLD') decommissioned\nDEFINE QLOCAL('LIVE.Q')", FormatMQSC},
		{"unknown", "hello world\nfoo=bar", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectFormat([]byte(tt.in)); got != tt.want {
				t.Fatalf("DetectFormat(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestConvertMQSC(t *testing.T) {
	mqsc := `
* Sample MQSC export
DEFINE QLOCAL('ORDER.REQUEST') MAXDEPTH(5000) DEFPSIST(YES)
DEFINE QLOCAL(ORDER.REPLY) +
       MAXDEPTH(5000)
DEFINE QREMOTE('PARTNER.OUT') RNAME(PARTNER.IN) RQMNAME(QM2)
DEFINE QALIAS('ORDERS') TARGET(ORDER.REQUEST)
DEFINE TOPIC('EVENTS') TOPICSTR('events/orders')
DEFINE QLOCAL('SYSTEM.DEAD.LETTER.QUEUE')
ALTER QLOCAL('ORDER.REQUEST') MAXDEPTH(9999)
`
	res, err := Convert(ConvertRequest{Name: "mq import", Data: []byte(mqsc)})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Format != FormatMQSC {
		t.Fatalf("format = %q, want mqsc", res.Format)
	}
	if res.Script.Protocol != "jms-ibmmq" {
		t.Fatalf("protocol = %q, want jms-ibmmq", res.Script.Protocol)
	}
	if res.Script.Config.JMS.Provider != "ibmmq" {
		t.Fatalf("provider = %q, want ibmmq", res.Script.Config.JMS.Provider)
	}
	if !res.Script.Config.JMS.Mock {
		t.Fatalf("expected mock mode")
	}
	// SYSTEM.* skipped; ALTER not double-counted (DEFINE already captured ORDER.REQUEST once).
	if res.Stats.Queues != 4 {
		t.Fatalf("queues = %d, want 4 (ORDER.REQUEST, ORDER.REPLY, PARTNER.OUT, ORDERS)", res.Stats.Queues)
	}
	if res.Stats.Topics != 1 {
		t.Fatalf("topics = %d, want 1", res.Stats.Topics)
	}
	// Each destination yields a publish + consume action.
	if got, want := len(res.Script.Actions), (4+1)*2; got != want {
		t.Fatalf("actions = %d, want %d", got, want)
	}
	assertHasAction(t, res.Script.Actions, "jms_publish", "ORDER.REQUEST")
	assertHasAction(t, res.Script.Actions, "jms_consume", "EVENTS")
	// Topic destination_type carried through.
	for _, a := range res.Script.Actions {
		if a.Target == "EVENTS" {
			if a.Params["destination_type"] != "topic" {
				t.Fatalf("EVENTS destination_type = %v, want topic", a.Params["destination_type"])
			}
		}
	}
	// IBM MQ review warning present.
	if !containsSubstr(res.Warnings, "IBM MQ runtime adapter is not built") {
		t.Fatalf("missing IBM MQ review warning, got %v", res.Warnings)
	}
	// Generated script must validate (mock mode, jms-ibmmq).
	if issues := dsl.ValidateScriptIssues(res.Script); len(issues) > 0 {
		t.Fatalf("generated MQSC script has validation issues: %v", issues)
	}
}

func TestConvertJNDI(t *testing.T) {
	jndi := `
# JNDI fscontext bindings
connectionFactory.cf = amqps://broker.example.test:5671
java.naming.factory.initial = org.apache.qpid.jms.jndi.JmsInitialContextFactory
queue.OrderQueue = ORDER.QUEUE
queue.ReplyQueue : REPLY.QUEUE
topic.EventTopic = EVENTS.TOPIC
solace.messageVPN = prod-vpn
`
	res, err := Convert(ConvertRequest{Data: []byte(jndi)})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Format != FormatJNDI {
		t.Fatalf("format = %q, want jndi", res.Format)
	}
	if res.Script.Protocol != "jms-solace" {
		t.Fatalf("protocol = %q, want jms-solace", res.Script.Protocol)
	}
	if res.Script.Config.JMS.Provider != "solace" {
		t.Fatalf("provider = %q, want solace", res.Script.Config.JMS.Provider)
	}
	if res.Script.Config.JMS.BrokerURL != "amqps://broker.example.test:5671" {
		t.Fatalf("broker_url = %q, want the connectionFactory uri", res.Script.Config.JMS.BrokerURL)
	}
	if res.Script.Config.JMS.MessageVPN != "prod-vpn" {
		t.Fatalf("message_vpn = %q, want prod-vpn", res.Script.Config.JMS.MessageVPN)
	}
	if res.Stats.Queues != 2 || res.Stats.Topics != 1 {
		t.Fatalf("queues=%d topics=%d, want 2/1", res.Stats.Queues, res.Stats.Topics)
	}
	assertHasAction(t, res.Script.Actions, "jms_publish", "ORDER.QUEUE")
	assertHasAction(t, res.Script.Actions, "jms_consume", "EVENTS.TOPIC")
	if !containsSubstr(res.Warnings, "JNDI bindings do not record the JMS provider") {
		t.Fatalf("missing JNDI provider warning, got %v", res.Warnings)
	}
	if issues := dsl.ValidateScriptIssues(res.Script); len(issues) > 0 {
		t.Fatalf("generated JNDI script has validation issues: %v", issues)
	}
}

func TestConvertMaxDestinations(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("DEFINE QLOCAL('Q")
		b.WriteByte(byte('0' + i))
		b.WriteString("')\n")
	}
	res, err := Convert(ConvertRequest{Data: []byte(b.String()), MaxDestinations: 3})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Stats.Queues != 10 {
		t.Fatalf("queues = %d, want 10 (all counted)", res.Stats.Queues)
	}
	if got := len(res.Script.Actions); got != 3*2 {
		t.Fatalf("actions = %d, want 6 (3 capped destinations x publish+consume)", got)
	}
	if !containsSubstr(res.Warnings, "seeded the first 3") {
		t.Fatalf("missing cap warning, got %v", res.Warnings)
	}
}

func TestConvertMQSCSkipsCommentLines(t *testing.T) {
	mqsc := "* DEFINE QLOCAL('OLD.QUEUE') -- decommissioned\nDEFINE QLOCAL('LIVE.Q')\n"
	res, err := Convert(ConvertRequest{Data: []byte(mqsc)})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Stats.Queues != 1 {
		t.Fatalf("queues = %d, want 1 (commented-out DEFINE must be skipped)", res.Stats.Queues)
	}
	assertHasAction(t, res.Script.Actions, "jms_publish", "LIVE.Q")
	for _, a := range res.Script.Actions {
		if a.Target == "OLD.QUEUE" {
			t.Fatal("OLD.QUEUE from a comment line must not be imported")
		}
	}
}

func TestConvertSanitizedNameCollision(t *testing.T) {
	// "A/B" and "A%B" are distinct MQ destinations but sanitize to the same base.
	mqsc := "DEFINE QLOCAL('A/B')\nDEFINE QLOCAL('A%B')\n"
	res, err := Convert(ConvertRequest{Data: []byte(mqsc)})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Stats.Queues != 2 {
		t.Fatalf("queues = %d, want 2", res.Stats.Queues)
	}
	seen := map[string]bool{}
	for _, a := range res.Script.Actions {
		if seen[a.Name] {
			t.Fatalf("duplicate action name %q — names must be unique", a.Name)
		}
		seen[a.Name] = true
	}
	// Both real wire destinations must survive as distinct targets.
	targets := map[string]bool{}
	for _, a := range res.Script.Actions {
		targets[a.Target] = true
	}
	if !targets["A/B"] || !targets["A%B"] {
		t.Fatalf("expected both A/B and A%%B targets, got %v", targets)
	}
	if issues := dsl.ValidateScriptIssues(res.Script); len(issues) > 0 {
		t.Fatalf("collision script has validation issues: %v", issues)
	}
}

func TestConvertJNDISelectsCorrectConnectionAndVPN(t *testing.T) {
	jndi := `connectionFactory.names = cf1
connectionFactory.cf1 = amqps://real.broker:5671
java.naming.security.principal = messagevpn-admin
solace.messageVPN = prod-vpn
queue.OrderQueue = ORDER.QUEUE
`
	res, err := Convert(ConvertRequest{Data: []byte(jndi)})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got := res.Script.Config.JMS.BrokerURL; got != "amqps://real.broker:5671" {
		t.Fatalf("broker_url = %q, want the URI-shaped connectionFactory value", got)
	}
	if got := res.Script.Config.JMS.MessageVPN; got != "prod-vpn" {
		t.Fatalf("message_vpn = %q, want prod-vpn (key must end in messageVPN)", got)
	}
}

func TestConvertConsumeTimeoutIsNumeric(t *testing.T) {
	res, err := Convert(ConvertRequest{Data: []byte("queue.Q = ORDER.Q"), Format: FormatJNDI})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, a := range res.Script.Actions {
		if a.Type != "jms_consume" {
			continue
		}
		v, ok := a.Params["timeout_ms"]
		if !ok {
			t.Fatal("consume action missing timeout_ms")
		}
		if _, isStr := v.(string); isStr {
			t.Fatalf("timeout_ms is a string %v — the JMS runtime only honors numeric timeouts", v)
		}
		if iv, isInt := v.(int); !isInt || iv != 1000 {
			t.Fatalf("timeout_ms = %#v, want int 1000", v)
		}
	}
}

func TestConvertErrors(t *testing.T) {
	if _, err := Convert(ConvertRequest{Data: nil}); err == nil {
		t.Fatal("expected error on empty data")
	}
	if _, err := Convert(ConvertRequest{Data: []byte("random text with no broker config")}); err == nil {
		t.Fatal("expected error on unrecognized format")
	}
	// Recognized format but no destinations.
	if _, err := Convert(ConvertRequest{Data: []byte("queue. = \ntopic.="), Format: FormatJNDI}); err == nil {
		t.Fatal("expected error when no destinations found")
	}
}

func assertHasAction(t *testing.T, actions []dsl.Action, typ, target string) {
	t.Helper()
	for _, a := range actions {
		if a.Type == typ && a.Target == target {
			return
		}
	}
	t.Fatalf("no %s action targeting %q found", typ, target)
}

func containsSubstr(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
