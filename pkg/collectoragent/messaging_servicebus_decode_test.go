package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func serviceBusDecodeTestRoute(t *testing.T, subscription bool) serviceBusManagementRoute {
	t.Helper()
	target := serviceBusConfigTestTarget(t)
	if subscription {
		target.Scope.Entities = []analysis.ManagementEntity{{ServiceBus: &analysis.ServiceBusManagementEntity{Topic: "events/west", Subscription: "workers"}}}
	}
	if err := prepareServiceBusManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	route, err := serviceBusRouteForEntity(&target, target.Scope.Entities[0])
	if err != nil {
		t.Fatal(err)
	}
	return route
}
func serviceBusDecodeTestXML(route serviceBusManagementRoute, fields string) string {
	typeName, title := "QueueDescription", route.entity.ServiceBus.Queue
	if route.subscription {
		typeName, title = "SubscriptionDescription", route.entity.ServiceBus.Subscription
	}
	return `<?xml version="1.0" encoding="utf-8"?><entry xmlns="` + serviceBusAtomNamespace + `"><id>` + route.requestURL + `</id><title type="text">` + title + `</title><content type="application/xml"><` + typeName + ` xmlns="` + serviceBusConnectNamespace + `" xmlns:i="` + serviceBusInstanceNamespace + `">` + fields + `</` + typeName + `></content></entry>`
}
func serviceBusDecodeTestCounts(fields string) string {
	return `<CountDetails xmlns:c="` + serviceBusCountNamespace + `">` + fields + `</CountDetails>`
}

func TestServiceBusDescriptionExactPresenceAndSecretProjection(t *testing.T) {
	route := serviceBusDecodeTestRoute(t, false)
	fields := `<MessageCount>9007199254740993</MessageCount><SizeInBytes>9223372036854775807</SizeInBytes>` + serviceBusDecodeTestCounts(`<c:ActiveMessageCount>2147483648</c:ActiveMessageCount><c:ScheduledMessageCount>0</c:ScheduledMessageCount><c:DeadLetterMessageCount>1</c:DeadLetterMessageCount><c:TransferMessageCount>2</c:TransferMessageCount><c:TransferDeadLetterMessageCount>3</c:TransferDeadLetterMessageCount>`) + `<AuthorizationRules><AuthorizationRule><PrimaryKey>CANARY_PRIVATE_KEY</PrimaryKey><SecondaryKey>CANARY_SECONDARY_KEY</SecondaryKey></AuthorizationRule></AuthorizationRules><UpdatedAt>2099-01-01T00:00:00Z</UpdatedAt>`
	result, err := decodeServiceBusManagementDescription(context.Background(), []byte(serviceBusDecodeTestXML(route, fields)), route)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		actual analysis.ManagementInteger
		want   string
	}{{result.Total, "9007199254740993"}, {result.SizeBytes, "9223372036854775807"}, {result.Active, "2147483648"}, {result.Scheduled, "0"}, {result.DeadLetter, "1"}, {result.Transfer, "2"}, {result.TransferDeadLetter, "3"}} {
		if pair.actual.State != analysis.ManagementStateObserved || pair.actual.Value == nil || *pair.actual.Value != pair.want {
			t.Fatal("exact source population lost", pair.want)
		}
	}
	if result.Deferred.State != analysis.ManagementStateUnsupported || result.Deferred.Value != nil {
		t.Fatal("deferred population invented")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"CANARY", "PrimaryKey", "UpdatedAt", "2099-01-01"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("description metadata leaked")
		}
	}
}
func TestServiceBusDescriptionOmissionNilAndSubscriptionQualifications(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		route := serviceBusDecodeTestRoute(t, subscription)
		fields := `<MessageCount i:nil="true"/>` + serviceBusDecodeTestCounts(`<c:ActiveMessageCount>000</c:ActiveMessageCount><c:ScheduledMessageCount>8</c:ScheduledMessageCount>`)
		result, err := decodeServiceBusManagementDescription(context.Background(), []byte(serviceBusDecodeTestXML(route, fields)), route)
		if err != nil {
			t.Fatal(err)
		}
		if result.Total.Value != nil || result.Total.Reason != "not_observed" || result.Active.Value == nil || *result.Active.Value != "0" || result.DeadLetter.Value != nil {
			t.Fatal("omitted/nil data became default numeric evidence")
		}
		if subscription && (result.Scheduled.State != analysis.ManagementStateUnsupported || result.Scheduled.Value != nil || result.SizeBytes.State != analysis.ManagementStateUnsupported) {
			t.Fatal("unqualified subscription fields invented")
		}
		if !subscription && (result.Scheduled.Value == nil || *result.Scheduled.Value != "8" || result.SizeBytes.Reason != "not_observed") {
			t.Fatal("queue presence profile lost")
		}
		aliasNil := `<MessageCount xmlns:nil="` + serviceBusInstanceNamespace + `" nil:nil="true"/>`
		result, err = decodeServiceBusManagementDescription(context.Background(), []byte(serviceBusDecodeTestXML(route, aliasNil)), route)
		if err != nil || result.Total.Value != nil {
			t.Fatal("qualified nil namespace alias rejected", err)
		}
		result, err = decodeServiceBusManagementDescription(context.Background(), []byte(serviceBusDecodeTestXML(route, `<CountDetails i:nil="true"/>`)), route)
		if err != nil || result.Active.Value != nil {
			t.Fatal("nil container fabricated fields", err)
		}
	}
}
func TestServiceBusDescriptionRejectsMalformedNumbersAndDuplicateFields(t *testing.T) {
	route := serviceBusDecodeTestRoute(t, false)
	for _, value := range []string{"", "-1", "+1", "1.5", "1e2", "9223372036854775808", "private-error", "  ", strings.Repeat("1", 33)} {
		if _, err := decodeServiceBusManagementDescription(context.Background(), []byte(serviceBusDecodeTestXML(route, `<MessageCount>`+value+`</MessageCount>`)), route); err == nil {
			t.Fatal("malformed count accepted", value)
		}
	}
	for _, fields := range []string{`<MessageCount>1</MessageCount><MessageCount>2</MessageCount>`, `<SizeInBytes>1</SizeInBytes><SizeInBytes>2</SizeInBytes>`, serviceBusDecodeTestCounts(`<c:ActiveMessageCount>1</c:ActiveMessageCount><c:ActiveMessageCount>2</c:ActiveMessageCount>`), `<CountDetails/><CountDetails/>`, `<MessageCount i:nil="true">0</MessageCount>`, `<MessageCount i:nil="maybe"/>`, `<MessageCount><Nested>0</Nested></MessageCount>`, `<CountDetails i:nil="true"><ActiveMessageCount>0</ActiveMessageCount></CountDetails>`, `<CountDetails i:nil="true">123</CountDetails>`, `<MessageCount p:nil="true">0</MessageCount>`, `<MessageCount nil="true">0</MessageCount>`} {
		if _, err := decodeServiceBusManagementDescription(context.Background(), []byte(serviceBusDecodeTestXML(route, fields)), route); err == nil {
			t.Fatal("ambiguous count presence accepted")
		}
	}
}
func TestServiceBusDescriptionRejectsWrongEnvelopeIdentityNamespacesAndDirectives(t *testing.T) {
	route := serviceBusDecodeTestRoute(t, false)
	valid := serviceBusDecodeTestXML(route, `<MessageCount>0</MessageCount>`)
	cases := []string{
		strings.Replace(valid, serviceBusAtomNamespace, "urn:wrong", 1),
		strings.Replace(valid, serviceBusConnectNamespace, "urn:wrong", 1),
		strings.Replace(valid, "QueueDescription", "SubscriptionDescription", -1),
		strings.Replace(valid, route.requestURL, "https://other.example.test/orders?api-version=2021-05", 1),
		strings.Replace(valid, "<title type=\"text\">orders</title>", "<title>other</title>", 1),
		strings.Replace(valid, "<MessageCount>", "<a:MessageCount xmlns:a=\""+serviceBusAtomNamespace+"\">", 1),
		valid + `<entry xmlns="` + serviceBusAtomNamespace + `"/>`,
		valid + `unparsed content`,
		strings.Replace(valid, `<content type="application/xml">`, `<content type="application/json">`, 1),
		strings.Replace(valid, "</content>", `<QueueDescription xmlns="`+serviceBusConnectNamespace+`"/></content>`, 1),
		strings.Replace(valid, `<?xml version="1.0" encoding="utf-8"?>`, `<!DOCTYPE entry [<!ENTITY private "CANARY_PRIVATE">]>`, 1),
		strings.Replace(valid, `<?xml version="1.0" encoding="utf-8"?>`, `<?other hidden="CANARY_PRIVATE"?>`, 1),
		strings.Replace(valid, `encoding="utf-8"`, `encoding="UTF-16"`, 1),
		strings.Replace(valid, `<MessageCount>0</MessageCount>`, `<MessageCount>&unknown;</MessageCount>`, 1),
	}
	for _, body := range cases {
		if _, err := decodeServiceBusManagementDescription(context.Background(), []byte(body), route); err == nil {
			t.Fatal("wrong identity or XML grammar accepted")
		}
	}
	for _, fields := range []string{`<MessageCount>0</MessageCount>`, serviceBusDecodeTestCounts(`<c:ActiveMessageCount>0</c:ActiveMessageCount>`)} {
		if _, err := decodeServiceBusManagementDescription(context.Background(), []byte(serviceBusDecodeTestXML(route, fields)), route); err != nil {
			t.Fatal("valid qualified namespace rejected", err)
		}
	}
}
func TestServiceBusDescriptionBoundedFeedNeverExpandsInventory(t *testing.T) {
	route := serviceBusDecodeTestRoute(t, false)
	body := `<feed xmlns="` + serviceBusAtomNamespace + `"><entry><id>https://unapproved.example.test/private</id><content type="application/xml"><QueueDescription xmlns="` + serviceBusConnectNamespace + `"><MessageCount>999</MessageCount></QueueDescription></content></entry></feed>`
	result, err := decodeServiceBusManagementDescription(context.Background(), []byte(body), route)
	if err != nil || result.Total.Value != nil || result.Active.Value != nil || result.Total.Reason != "not_observed" {
		t.Fatal("discovered feed became approved evidence", err)
	}
}
func TestServiceBusDescriptionBoundariesAndCancelledParse(t *testing.T) {
	route := serviceBusDecodeTestRoute(t, false)
	minimal := `<entry xmlns="` + serviceBusAtomNamespace + `"><content type="application/xml"><QueueDescription xmlns="` + serviceBusConnectNamespace + `"></QueueDescription></content></entry>`
	atLimit := strings.Repeat(" ", serviceBusDescriptionMaxBodyBytes-len(minimal)) + minimal
	if _, err := decodeServiceBusManagementDescription(context.Background(), []byte(atLimit), route); err != nil {
		t.Fatal("exact body limit rejected", err)
	}
	if _, err := decodeServiceBusManagementDescription(context.Background(), []byte(atLimit+" "), route); err == nil {
		t.Fatal("body overflow accepted")
	}
	// Three envelope starts plus these static descendants exercise exact depth.
	for _, extra := range []int{serviceBusDescriptionMaxDepth - 3, serviceBusDescriptionMaxDepth - 2} {
		fields := strings.Repeat("<Metadata>", extra) + strings.Repeat("</Metadata>", extra)
		_, err := decodeServiceBusManagementDescription(context.Background(), []byte(serviceBusDecodeTestXML(route, fields)), route)
		if extra == serviceBusDescriptionMaxDepth-3 && err != nil || extra > serviceBusDescriptionMaxDepth-3 && err == nil {
			t.Fatal("depth bound failed", extra, err)
		}
	}
	for _, comments := range []int{serviceBusDescriptionMaxTokens - 6, serviceBusDescriptionMaxTokens - 5} {
		body := strings.Replace(minimal, "</QueueDescription>", strings.Repeat("<!--ignored-->", comments)+"</QueueDescription>", 1)
		_, err := decodeServiceBusManagementDescription(context.Background(), []byte(body), route)
		if comments == serviceBusDescriptionMaxTokens-6 && err != nil || comments > serviceBusDescriptionMaxTokens-6 && err == nil {
			t.Fatal("token bound failed", comments, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := decodeServiceBusManagementDescription(ctx, []byte(minimal), route); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled parser accepted values", err)
	}
}
