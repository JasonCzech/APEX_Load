package jmeter

// converter_parity.go — HTTP client and thread-group behaviour that JMeter
// applies implicitly. The plan never states these settings, so leaving them
// at APEX defaults would silently change how the converted script behaves.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// jmeterSampleErrorReason gates a thread group whose sample-error action
// stops a thread or the test, which no APEX error policy does.
const jmeterSampleErrorReason = "unsupported_jmeter_sample_error_action"

// jmeterCookieManagersInScope returns the enabled Cookie Managers that apply
// to the thread group: those on its ancestors and those anywhere below it.
// APEX keeps one cookie jar per script, so a manager scoped to a controller
// still turns cookies on for the whole script.
func jmeterCookieManagersInScope(trees []*treeNode, tg *treeNode) []*treeNode {
	var managers []*treeNode
	ancestors, _ := jmeterAncestorScopeConfigs(trees, tg)
	for _, config := range ancestors {
		if config.Element.Name == "CookieManager" {
			managers = append(managers, config)
		}
	}
	if tg != nil {
		managers = append(managers, findEnabledTreeNodes(tg.Children, "CookieManager")...)
	}
	return managers
}

// applyJMeterHTTPClientParity sets the HTTP client behaviour JMeter's core
// HTTP samplers have without any configuration: HTTP/1.1 only, any server
// certificate accepted, no cookies without a Cookie Manager, and the
// sampler timeouts that could not become an action timeout disclosed.
// location is the group's script identity, which tells a repeated group name
// apart and names the group's own source mapping.
func applyJMeterHTTPClientParity(script *dsl.Script, tg *treeNode, location string, cookieManagers []*treeNode, timeouts jmeterSamplerTimeoutStats, diagnostics *diag.Collector) {
	if script == nil || diagnostics == nil {
		return
	}
	httpConfig := &script.Config.HTTP
	// The HttpClient4 and Java implementations speak HTTP/1.1 only; HTTP/2
	// needs a separate plugin sampler. Without a version APEX offers h2 over
	// ALPN, which changes framing and header encoding.
	if httpConfig.Version == "" {
		httpConfig.Version = "1.1"
	}
	// USER DECISION: match JMeter, which trusts every server certificate, and
	// say so plainly rather than let an internal certificate fail every request.
	if !httpConfig.TLS.InsecureSkipVerify && strings.TrimSpace(httpConfig.TLS.CACertFile) == "" {
		httpConfig.TLS.InsecureSkipVerify = true
		diagnostics.Add(diag.Warning, "JMeter HTTP TLS", location, "JMeter's HTTP samplers accept any server certificate without checking who issued it or the host name it names, so certificate verification is turned off for this script to match (config.http.tls.insecure_skip_verify is true): its requests, and any credential they carry, go to servers whose certificates are not checked. OAuth token requests always verify certificates. To verify certificates, set insecure_skip_verify to false and, for an internal or self-signed certificate, point config.http.tls.ca_cert_file at its CA bundle. If the script keeps an imported credential (such as a literal Authorization header or a basic-auth password), its TLS settings are held with that credential and such an edit is refused: bind the credential to a runtime secret first, or ask a system administrator to allow TLS edits (Access > Import credential policy). Importing the plan again turns verification off again")
	}
	if len(cookieManagers) == 0 {
		if httpConfig.CookiesEnabled == nil {
			disabled := false
			httpConfig.CookiesEnabled = &disabled
			diagnostics.Add(diag.Info, "JMeter HTTP cookies", location, "the plan has no HTTP Cookie Manager, so JMeter neither stores nor sends cookies; cookie handling is turned off for this script to match (config.http.cookies_enabled is false)")
		}
	} else if jmeterCookiesClearEachIteration(cookieManagers, tg) && httpConfig.ClearCookiesPerIter == nil {
		clear := true
		httpConfig.ClearCookiesPerIter = &clear
	}
	// One disclosure per group lists every kind: separate ones with the same
	// element and source would collapse into the first in review evidence.
	var timeoutNotes []string
	if timeouts.untimed > 0 {
		timeoutNotes = append(timeoutNotes, fmt.Sprintf("%d HTTP sampler(s) set no connect or response timeout, so JMeter waits for them without limit; APEX applies its defaults of %s to connect and %s per request, so a slower response is recorded as an error. Raise config.http.connect_timeout_ms or request_timeout_ms (up to %s) if the target can be that slow", timeouts.untimed, httpprotocol.DefaultConnectTimeout, httpprotocol.DefaultRequestTimeout, httpprotocol.MaxConfiguredTimeout))
	}
	if timeouts.connectOnly > 0 {
		timeoutNotes = append(timeoutNotes, fmt.Sprintf("%d HTTP sampler(s) set only a connect timeout. JMeter applies it to the connect phase alone and then waits for the response without limit, while an APEX action timeout bounds the whole request, so the value is not applied: APEX uses its defaults of %s to connect and %s per request. Set config.http.connect_timeout_ms to the JMeter connect timeout, and raise request_timeout_ms (up to %s) if responses can be that slow", timeouts.connectOnly, httpprotocol.DefaultConnectTimeout, httpprotocol.DefaultRequestTimeout, httpprotocol.MaxConfiguredTimeout))
	}
	if timeouts.variable > 0 {
		timeoutNotes = append(timeoutNotes, fmt.Sprintf("%d HTTP sampler(s) take a timeout from a variable or function, which JMeter resolves at run time; the import does not resolve it, so APEX applies its defaults of %s to connect and %s per request. Set a variable response timeout as the action's timeout_ms or config.http.request_timeout_ms, and a variable connect timeout only as config.http.connect_timeout_ms, because an APEX action timeout bounds the whole request", timeouts.variable, httpprotocol.DefaultConnectTimeout, httpprotocol.DefaultRequestTimeout))
	}
	if len(timeoutNotes) > 0 {
		diagnostics.Add(diag.Info, "JMeter HTTP timeouts", location, strings.Join(timeoutNotes, ". "))
	}
}

// jmeterCookiesClearEachIteration reports whether a Cookie Manager in scope
// clears its cookies when an iteration starts. A manager that follows the
// thread group does so when the group starts a new user each iteration;
// otherwise its own clear-each-iteration flag decides.
func jmeterCookiesClearEachIteration(cookieManagers []*treeNode, tg *treeNode) bool {
	newUserEachIteration := false
	if tg != nil && tg.Element != nil && tg.Element.Name == "ThreadGroup" {
		newUserEachIteration = strings.EqualFold(prop(tg.Element, "ThreadGroup.same_user_on_next_iteration"), "false")
	}
	for _, manager := range cookieManagers {
		if strings.EqualFold(prop(manager.Element, "CookieManager.controlledByThreadGroup"), "true") {
			if newUserEachIteration {
				return true
			}
			continue
		}
		if strings.EqualFold(prop(manager.Element, "CookieManager.clearEachIteration"), "true") {
			return true
		}
	}
	return false
}

// applyJMeterThreadGroupErrorPolicy records the thread group's sample-error
// action on the script, so the decision travels with it instead of coming
// from whatever the scenario defaults to.
func applyJMeterThreadGroupErrorPolicy(tg *treeNode, script *dsl.Script, diagnostics *diag.Collector, sourceFileName string, mappings *[]sourceview.Mapping) {
	if tg == nil || tg.Element == nil || tg.Element.Name != "ThreadGroup" || script == nil {
		return
	}
	action := strings.ToLower(strings.TrimSpace(prop(tg.Element, "ThreadGroup.on_sample_error")))
	switch action {
	case "", "continue":
		script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(true)
		return
	case "startnextloop":
		// APEX ends the iteration on the first failed request when
		// continue-on-error is off, which is JMeter's "Start Next Thread Loop".
		script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(false)
		return
	}
	// Stopping the thread or the whole test on the first error has no APEX
	// equivalent. Ending the iteration is the closest behaviour once the
	// operator accepts it, so it is set before the gate.
	script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(false)
	location := jmeterElementLocation(tg.Element)
	what := map[string]string{
		"stopthread":  "stops that VUser",
		"stoptest":    "stops the test once running samples finish",
		"stoptestnow": "stops the test immediately",
	}[action]
	if what == "" {
		what = fmt.Sprintf("takes the unrecognized action %q", action)
	}
	// The blocker names the script, as its review-only mapping does, so the
	// gate it describes is counted once.
	diagnostics.Add(diag.Blocker, "JMeter ThreadGroup", script.Name, "after a sampler error JMeter "+what+"; APEX can only continue or end the iteration, so the script is set to end the iteration (config.error_handling.continue_on_error is false) and is gated until you accept that or choose another policy, then remove the review step")
	index := appendJMeterSourceReviewPlaceholder(script, "ThreadGroup sample error action", location, sourceFileName, jmeterSampleErrorReason)
	*mappings = append(*mappings, jmeterSourceMapping(sourceFileName, tg.Element, "ThreadGroup", fmt.Sprintf("actions[%d]", index), script.Name, "controller", "review_only", "The thread group's action after a sampler error has no APEX equivalent."))
}

// jmeterThreadGroupPopulation keeps the thread group's load shape in the
// script's import metadata, so a script saved without the scenario draft does
// not lose the users, ramp and duration the plan defined. Literal values are
// numbers; any other value keeps its source text under a "_source" key.
func jmeterThreadGroupPopulation(tg *treeNode) map[string]any {
	if tg == nil || tg.Element == nil || tg.Element.Name != "ThreadGroup" {
		return nil
	}
	population := map[string]any{}
	add := func(key, property string) {
		raw := prop(tg.Element, property)
		if raw == "" {
			return
		}
		if value, err := strconv.Atoi(raw); err == nil {
			population[key] = value
			return
		}
		population[key+"_source"] = raw
	}
	add("num_threads", "ThreadGroup.num_threads")
	add("ramp_time_seconds", "ThreadGroup.ramp_time")
	add("loops", "LoopController.loops")
	if strings.EqualFold(prop(tg.Element, "LoopController.continue_forever"), "true") {
		population["loops"] = -1
		delete(population, "loops_source")
	}
	if strings.EqualFold(prop(tg.Element, "ThreadGroup.scheduler"), "true") {
		population["scheduler"] = true
		add("duration_seconds", "ThreadGroup.duration")
		add("delay_seconds", "ThreadGroup.delay")
	}
	if len(population) == 0 {
		return nil
	}
	return population
}

// appendJMeterContributorMapping records a configuration element that shapes
// the generated script without becoming an action of its own, so source
// review can show where its values went. The "config" kind keeps it out of
// the mapped/review tallies, which count behaviours, not settings.
func appendJMeterContributorMapping(mappings *[]sourceview.Mapping, sourceFileName string, element *xmlNode, targetPath, targetName string) {
	if mappings == nil || element == nil {
		return
	}
	notes := map[string]string{
		"TestPlan":          "Test plan variables were expanded into the generated requests, and its thread groups became scripts.",
		"ThreadGroup":       "Thread group users, ramp-up, loops and error handling shaped the script's run logic and the scenario draft.",
		"Arguments":         "User Defined Variables were expanded into the generated requests.",
		"HeaderManager":     "HTTP Header Manager headers were added to the requests in its scope.",
		"ConfigTestElement": "HTTP Request Defaults filled in the requests' protocol, host, port, path and timeouts.",
		"CookieManager":     "HTTP Cookie Manager turned on cookie handling for the script.",
	}[element.Name]
	if notes == "" {
		return
	}
	// Only the HTTP Request Defaults flavour of ConfigTestElement feeds requests.
	if element.Name == "ConfigTestElement" && element.Attrs["guiclass"] != "HttpDefaultsGui" && prop(element, "HTTPSampler.domain") == "" {
		return
	}
	*mappings = append(*mappings, jmeterSourceMapping(sourceFileName, element, element.Name, targetPath, targetName, "config", "converted", notes))
}

// jmeterEnabledPlanFor returns the enabled Test Plan that holds tg.
func jmeterEnabledPlanFor(plans []*treeNode, tg *treeNode) *treeNode {
	for _, plan := range plans {
		if plan == tg {
			return plan
		}
		for _, group := range findEnabledTreeNodes(plan.Children, tg.Element.Name) {
			if group == tg {
				return plan
			}
		}
	}
	return nil
}
