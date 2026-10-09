package sample

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// normalizeRecordedExchanges applies the recorded-traffic rules shared with
// the proxy recorder (pkg/convert/requestimport) before correlation:
//   - the 401 leg of an NTLM or Negotiate handshake is dropped, because the
//     retry that follows it is the real request;
//   - single-use NTLM/Negotiate Authorization tokens are removed; the script
//     answers those hosts' challenges with negotiate_any auth (see
//     integratedAuthHosts and requestimport.ApplyIntegratedAuth);
//   - cookies a request carried only because an earlier recorded response
//     set them are removed from its Cookie header, since the replay cookie
//     jar sends them; keeping both sends two values for one name.
func normalizeRecordedExchanges(exchanges []exchange, format string) ([]exchange, []string) {
	kept := make([]exchange, 0, len(exchanges))
	droppedLegs := 0
	for i := range exchanges {
		if isIntegratedAuthHandshakeExchange(exchanges, i) {
			droppedLegs++
			continue
		}
		kept = append(kept, exchanges[i])
	}
	origins := requestimport.NewCookieOrigins()
	jarCookies := 0
	for i := range kept {
		ex := &kept[i]
		if scheme := exchangeIntegratedAuthScheme(*ex); scheme != "" {
			ex.Headers = headersWithoutFold(ex.Headers, "Authorization")
			ex.HeaderList = headerListWithoutFold(ex.HeaderList, "Authorization")
			ex.ReviewFindings = append(ex.ReviewFindings, requestimport.IntegratedAuthFinding(format, fmt.Sprintf("entry-%d", ex.SourceEntryID), scheme))
		}
		jarCookies += leaveJarCookiesToTheJar(ex, origins)
		if ex.Response != nil {
			origins.Observe(ex.URL, responseSetCookies(ex.Response))
		}
	}
	var warnings []string
	if droppedLegs > 0 {
		warnings = append(warnings, fmt.Sprintf("%d NTLM/Negotiate handshake challenge leg(s) were dropped; the script answers those hosts' challenges with negotiate_any auth whose credentials must be configured", droppedLegs))
	}
	if jarCookies > 0 {
		warnings = append(warnings, fmt.Sprintf("%d recorded Cookie value(s) were left to the replay cookie jar because an earlier recorded response set them", jarCookies))
	}
	return kept, warnings
}

func isIntegratedAuthHandshakeExchange(exchanges []exchange, i int) bool {
	ex := exchanges[i]
	if ex.Response == nil || !requestimport.IsIntegratedAuthChallenge(ex.Response.Status, responseHeaderValues(ex.Response, "WWW-Authenticate")) {
		return false
	}
	if i+1 >= len(exchanges) {
		return false
	}
	next := exchanges[i+1]
	return strings.EqualFold(next.Method, ex.Method) && next.URL == ex.URL && exchangeIntegratedAuthScheme(next) != ""
}

func exchangeIntegratedAuthScheme(ex exchange) string {
	for _, entry := range ex.HeaderList {
		if strings.EqualFold(entry.Name, "Authorization") {
			if scheme := requestimport.IntegratedAuthScheme(entry.Value); scheme != "" {
				return scheme
			}
		}
	}
	for name, value := range ex.Headers {
		if strings.EqualFold(name, "Authorization") {
			if scheme := requestimport.IntegratedAuthScheme(value); scheme != "" {
				return scheme
			}
		}
	}
	return ""
}

// leaveJarCookiesToTheJar removes jar-provided cookies from the exchange's
// Cookie header(s) and returns how many were removed.
func leaveJarCookiesToTheJar(ex *exchange, origins *requestimport.CookieOrigins) int {
	removed := 0
	if len(ex.HeaderList) > 0 {
		filtered := ex.HeaderList[:0:0]
		var cookieValues []string
		for _, entry := range ex.HeaderList {
			if !strings.EqualFold(entry.Name, "Cookie") {
				filtered = append(filtered, entry)
				continue
			}
			remaining, n := origins.WithoutJarCookies(ex.URL, entry.Value)
			removed += n
			if remaining != "" {
				filtered = append(filtered, dsl.HeaderEntry{Name: entry.Name, Value: remaining})
				cookieValues = append(cookieValues, remaining)
			}
		}
		if removed == 0 {
			return 0
		}
		ex.HeaderList = filtered
		ex.Headers = headersWithoutFold(ex.Headers, "Cookie")
		if len(cookieValues) > 0 {
			ex.Headers["Cookie"] = strings.Join(cookieValues, "; ")
		}
		return removed
	}
	for name, value := range ex.Headers {
		if !strings.EqualFold(name, "Cookie") {
			continue
		}
		remaining, n := origins.WithoutJarCookies(ex.URL, value)
		if n == 0 {
			return 0
		}
		ex.Headers = headersWithoutFold(ex.Headers, "Cookie")
		if remaining != "" {
			ex.Headers[name] = remaining
		}
		return n
	}
	return removed
}

func responseSetCookies(resp *response) []string {
	return responseHeaderValues(resp, "Set-Cookie")
}

func responseHeaderValues(resp *response, name string) []string {
	var values []string
	for _, entry := range resp.HeaderList {
		if strings.EqualFold(entry.Name, name) {
			values = append(values, entry.Value)
		}
	}
	if len(values) > 0 {
		return values
	}
	for key, value := range resp.Headers {
		if strings.EqualFold(key, name) {
			values = append(values, value)
		}
	}
	return values
}

func exchangeHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// headersWithoutFold returns a copy of headers without name (any case); it
// never returns nil so callers can add to it.
func headersWithoutFold(headers map[string]string, name string) map[string]string {
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if !strings.EqualFold(key, name) {
			out[key] = value
		}
	}
	return out
}

func headerListWithoutFold(list dsl.HeaderList, name string) dsl.HeaderList {
	if len(list) == 0 {
		return list
	}
	out := make(dsl.HeaderList, 0, len(list))
	for _, entry := range list {
		if !strings.EqualFold(entry.Name, name) {
			out = append(out, entry)
		}
	}
	return out
}

// applyExchangeReviewFindings attaches an exchange's review findings to the
// action generated from it.
func applyExchangeReviewFindings(action *dsl.Action, ex exchange) {
	if len(ex.ReviewFindings) > 0 {
		importreview.ApplyToAction(action, ex.ReviewFindings)
	}
}

// integratedAuthHosts lists the hosts of requests recorded with an
// integrated-auth handshake.
func integratedAuthHosts(exchanges []exchange) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, ex := range exchanges {
		for _, finding := range ex.ReviewFindings {
			if finding.Code != requestimport.IntegratedAuthReasonCode {
				continue
			}
			if host := requestimport.IntegratedAuthHost(ex.URL); host != "" && !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}
