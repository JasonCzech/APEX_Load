package jmeter

import (
	"strings"
	"testing"
)

func TestMessagingXMLRequiresOneCompleteBoundedDocument(t *testing.T) {
	for _, source := range []string{
		`<jmeterTestPlan/><extra/>`,
		`<jmeterTestPlan/>trailing`,
		`<jmeterTestPlan/><!-- incomplete`,
		strings.Repeat("<node>", maxJMeterXMLDepth+1) + strings.Repeat("</node>", maxJMeterXMLDepth+1),
		"<root>" + strings.Repeat("<node/>", maxJMeterXMLNodes) + "</root>",
	} {
		if _, err := parseXML([]byte(source)); err == nil {
			t.Fatal("accepted incomplete or unbounded XML")
		}
	}
	for _, source := range []string{`<?xml version="1.0"?><jmeterTestPlan/><!-- complete -->`, strings.Repeat("<node>", maxJMeterXMLDepth) + strings.Repeat("</node>", maxJMeterXMLDepth)} {
		if _, err := parseXML([]byte(source)); err != nil {
			t.Fatal(err)
		}
	}
}
