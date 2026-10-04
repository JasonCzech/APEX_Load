package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func solaceDecodeFixture(t *testing.T, topic bool) (ManagementTarget, solaceManagementRoute) {
	t.Helper()
	target := solaceConfigTestTarget(t)
	if err := prepareSolaceManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	index := 0
	if topic {
		index = 2
	}
	route, err := solaceRouteForEntity(&target, target.Scope.Entities[index])
	if err != nil {
		t.Fatal(err)
	}
	return target, route
}
func solaceDecodeBody(t *testing.T, route solaceManagementRoute, count json.RawMessage) []byte {
	t.Helper()
	entity := route.entity.Solace
	data := map[string]any{"msgVpnName": entity.VPN, "deadMsgQueue": "dmq", "spooledMsgCount": uint64(999), "msgSpoolUsage": uint64(888)}
	if entity.Queue != "" {
		data["queueName"] = entity.Queue
	} else {
		data["topicEndpointName"] = entity.TopicEndpoint
	}
	body, err := json.Marshal(map[string]any{
		"meta": map[string]any{"responseCode": 200, "request": map[string]any{"method": "GET", "uri": route.requestURL}, "count": 1},
		"data": data, "collections": map[string]any{"msgs": map[string]any{"count": count}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func TestSolaceDescriptionExactDepthAndApprovedDMQ(t *testing.T) {
	for _, topic := range []bool{false, true} {
		for _, depth := range []string{"0", "9007199254740993", "9223372036854775807"} {
			target, route := solaceDecodeFixture(t, topic)
			body := solaceDecodeBody(t, route, json.RawMessage(depth))
			result, err := decodeSolaceManagementDescription(context.Background(), body, route, target.Scope)
			if err != nil || result.Depth.State != analysis.ManagementStateObserved || result.Depth.Value == nil || *result.Depth.Value != depth || result.DeadMessageQueue != "dmq" {
				t.Fatal("exact selected entity depth/relationship changed", err)
			}
		}
	}
}
func TestSolaceDescriptionAbsenceNeverBecomesZero(t *testing.T) {
	target, route := solaceDecodeFixture(t, false)
	base := string(solaceDecodeBody(t, route, json.RawMessage("0")))
	for name, body := range map[string]string{
		"null count":          strings.Replace(base, `"count":0`, `"count":null`, 1),
		"missing count":       strings.Replace(base, `"count":0`, `"other":0`, 1),
		"null messages":       strings.Replace(base, `"msgs":{"count":0}`, `"msgs":null`, 1),
		"missing messages":    strings.Replace(base, `"msgs":{"count":0}`, `"other":{"count":0}`, 1),
		"null collections":    strings.Replace(base, `"collections":{"msgs":{"count":0}}`, `"collections":null`, 1),
		"missing collections": strings.Replace(base, `"collections":`, `"unselected":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := decodeSolaceManagementDescription(context.Background(), []byte(body), route, target.Scope)
			if err != nil || result.Depth.State != analysis.ManagementStateMissing || result.Depth.Value != nil || result.Depth.Reason != "not_observed" {
				t.Fatal("missing provider depth became a value", err)
			}
		})
	}
	for _, relationship := range []string{`null`, `""`, `"not-approved"`} {
		body := strings.Replace(base, `"deadMsgQueue":"dmq"`, `"deadMsgQueue":`+relationship, 1)
		result, err := decodeSolaceManagementDescription(context.Background(), []byte(body), route, target.Scope)
		if err != nil || result.DeadMessageQueue != "" {
			t.Fatal("unavailable/out-of-scope relationship exposed", err)
		}
	}
	body := strings.Replace(base, `"deadMsgQueue":"dmq",`, "", 1)
	result, err := decodeSolaceManagementDescription(context.Background(), []byte(body), route, target.Scope)
	if err != nil || result.DeadMessageQueue != "" {
		t.Fatal("absent relationship inferred", err)
	}
	scope := target.Scope.Clone()
	scope.Entities[1].Solace.VPN = "other-vpn"
	result, err = decodeSolaceManagementDescription(context.Background(), []byte(base), route, scope)
	if err != nil || result.DeadMessageQueue != "" {
		t.Fatal("cross-VPN relationship exposed", err)
	}
}
func TestSolaceDescriptionRejectsInvalidEvidenceWithoutDisclosure(t *testing.T) {
	target, route := solaceDecodeFixture(t, false)
	base := string(solaceDecodeBody(t, route, json.RawMessage("0")))
	arrayData := strings.Replace(strings.Replace(base, `"data":{`, `"data":[{`, 1), `},"meta":`, `}],"meta":`, 1)
	for name, body := range map[string]string{
		"overflow":             strings.Replace(base, `"count":0`, `"count":9223372036854775808`, 1),
		"negative":             strings.Replace(base, `"count":0`, `"count":-1`, 1),
		"fraction":             strings.Replace(base, `"count":0`, `"count":1.0`, 1),
		"exponent":             strings.Replace(base, `"count":0`, `"count":1e3`, 1),
		"string integer":       strings.Replace(base, `"count":0`, `"count":"0"`, 1),
		"duplicate count":      strings.Replace(base, `"count":0`, `"count":0,"count":1`, 1),
		"duplicate unknown":    strings.Replace(base, `"data":{`, `"data":{"unknown":0,"unknown":1,`, 1),
		"duplicate collection": strings.Replace(base, `"msgs":{"count":0}`, `"msgs":{"count":0},"msgs":{}`, 1),
		"array collections":    strings.Replace(base, `"collections":{"msgs":{"count":0}}`, `"collections":[]`, 1),
		"array messages":       strings.Replace(base, `"msgs":{"count":0}`, `"msgs":[]`, 1),
		"data array":           arrayData,
		"wrong VPN":            strings.Replace(base, `"msgVpnName":"orders"`, `"msgVpnName":"other"`, 1),
		"wrong queue":          strings.Replace(base, `"queueName":"pending"`, `"queueName":"other"`, 1),
		"wrong kind":           strings.Replace(base, `"queueName":"pending"`, `"topicEndpointName":"pending"`, 1),
		"additional kind":      strings.Replace(base, `"queueName":"pending"`, `"queueName":"pending","topicEndpointName":"events"`, 1),
		"missing meta":         strings.Replace(base, `"meta":`, `"unselectedMeta":`, 1),
		"missing status":       strings.Replace(base, `"responseCode":200`, `"unselectedStatus":200`, 1),
		"wrong status":         strings.Replace(base, `"responseCode":200`, `"responseCode":403`, 1),
		"wrong method":         strings.Replace(base, `"method":"GET"`, `"method":"POST"`, 1),
		"paging":               strings.Replace(base, `"meta":{`, `"meta":{"paging":{"nextPageUri":"https://evil.example.test/","cursorQuery":"secret-canary"},`, 1),
		"raw error envelope":   strings.Replace(base, `"meta":{`, `"meta":{"error":"secret-canary",`, 1),
		"truncated":            base[:len(base)-1],
		"trailing document":    base + "{}",
		"XML":                  `<rpc><show><queue>secret-canary</queue></show></rpc>`,
		"invalid surrogate":    strings.Replace(base, `"data":{`, `"data":{"extra":"\uD800",`, 1),
		"invalid UTF8":         strings.Replace(base, `"data":{`, "\"data\":{\"extra\":\""+string([]byte{0xff})+"\",", 1),
		"numeric DMQ":          strings.Replace(base, `"deadMsgQueue":"dmq"`, `"deadMsgQueue":10`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeSolaceManagementDescription(context.Background(), []byte(body), route, target.Scope)
			if err == nil || strings.Contains(err.Error(), "secret-canary") {
				t.Fatal("invalid evidence accepted or provider detail disclosed", err)
			}
		})
	}
	for code, reason := range map[string]string{"6": "not_observed", "14": "unsupported", "72": "permission_denied", "999": "invalid"} {
		body := []byte(`{"meta":{"responseCode":400,"request":{"method":"GET"},"error":{"code":` + code + `,"status":"secret-canary","description":"secret-canary"}}}`)
		_, err := decodeSolaceManagementDescription(context.Background(), body, route, target.Scope)
		if kafkaManagementErrorReason(err) != reason || strings.Contains(err.Error(), "secret-canary") {
			t.Fatal("numeric error classification/disclosure changed", err)
		}
	}
}
func TestSolaceDescriptionBoundsAndCancellation(t *testing.T) {
	target, route := solaceDecodeFixture(t, false)
	base := string(solaceDecodeBody(t, route, json.RawMessage("0")))
	for name, body := range map[string]string{
		"body":  strings.Repeat(" ", solaceManagementBodyLimit+1),
		"depth": strings.Replace(base, `"data":{`, `"data":{"extra":`+strings.Repeat("[", 33)+"0"+strings.Repeat("]", 33)+",", 1),
		"nodes": strings.Replace(base, `"data":{`, `"data":{"extra":[`+strings.Repeat("null,", 8192)+"null],", 1),
		"text":  strings.Replace(base, `"data":{`, `"data":{"extra":"`+strings.Repeat("x", 4097)+`",`, 1),
		"key":   strings.Replace(base, `"data":{`, `"data":{"`+strings.Repeat("x", 4097)+`":0,`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeSolaceManagementDescription(context.Background(), []byte(body), route, target.Scope)
			if kafkaManagementErrorReason(err) != "budget_exhausted" {
				t.Fatal("analytical response bound did not fail explicitly", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := decodeSolaceManagementDescription(ctx, []byte(base), route, target.Scope); !errors.Is(err, context.Canceled) {
		t.Fatal("preexisting cancellation ignored")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	counted := &solaceCancelAfterChecksContext{Context: ctx, cancel: cancel, after: 15}
	if _, err := decodeSolaceManagementDescription(counted, []byte(base), route, target.Scope); !errors.Is(err, context.Canceled) {
		t.Fatal("in-parser cancellation ignored", err)
	}
	unapproved := route
	unapproved.entity = analysis.ManagementEntity{Solace: &analysis.SolaceManagementEntity{VPN: "orders", Queue: "other"}}
	if _, err := decodeSolaceManagementDescription(context.Background(), []byte(base), unapproved, target.Scope); kafkaManagementErrorReason(err) != "permission_denied" {
		t.Fatal("unapproved response identity could freshen source", err)
	}
	body := strings.Replace(base, `"data":{`, `"data":{"extra":"\uD83D\uDE00",`, 1)
	if _, err := decodeSolaceManagementDescription(context.Background(), []byte(body), route, target.Scope); err != nil {
		t.Fatal("valid Unicode pair rejected", err)
	}
}

type solaceCancelAfterChecksContext struct {
	context.Context
	cancel        context.CancelFunc
	checks, after int
}

func (c *solaceCancelAfterChecksContext) Err() error {
	c.checks++
	if c.checks >= c.after {
		c.cancel()
	}
	return c.Context.Err()
}
func TestSolaceLogoutRequiresSuccessfulFixedActionEnvelope(t *testing.T) {
	if err := decodeSolaceManagementLogout(context.Background(), []byte(`{"meta":{"responseCode":200,"request":{"method":"PUT"}},"data":{}}`)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{}`,
		`{"meta":{"responseCode":200,"request":{"method":"GET"}}}`,
		`{"meta":{"responseCode":200,"responseCode":200,"request":{"method":"PUT"}}}`,
		`{"meta":{"responseCode":403,"request":{"method":"PUT"}}}`,
		`{"meta":{"responseCode":200,"request":{"method":"PUT"},"error":{"code":72,"description":"secret-canary"}}}`,
		`{"meta":{"responseCode":200,"request":{"method":"PUT"},"paging":{}}}`,
	} {
		err := decodeSolaceManagementLogout(context.Background(), []byte(body))
		if err == nil || strings.Contains(err.Error(), "secret-canary") {
			t.Fatal("HTTP/action failure fabricated successful logout", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := decodeSolaceManagementLogout(ctx, []byte(`{"meta":{"responseCode":200,"request":{"method":"PUT"}}}`)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled action fabricated logout proof")
	}
}
