package enterprisearchive

import (
	"net/url"
	"strings"
)

func refinePortableProxySelection(rc *RuntimeConfig, sections map[string]map[string]string) {
	if rc == nil {
		return
	}
	web := findMergedSection(sections, "Proxy", "WEB", "Web")
	selected := func(key string) (bool, bool) {
		raw, ok := sectionValueCaseInsensitive(web, key)
		if !ok {
			return false, true
		}
		return parseBoolExplicit(raw)
	}
	browser, browserValid := selected("ProxyUseBrowser")
	pac, pacValid := selected("ProxyUseAutoConfigScript")
	custom, customValid := selected("ProxyUseProxyServer")
	parent, parentValid := selected("ProxyUseProxy")
	_, parentPresent := sectionValueCaseInsensitive(web, "ProxyUseProxy")
	conflict := !parentValid || (parentPresent && !parent && (browser || pac)) || !browserValid || !pacValid || !customValid || (browser && (custom || pac)) || (pac && custom)
	mode := strings.ToLower(runtimeSectionFirstNonBlank(web, "Mode", "ProxyMode", "Type"))
	if mode != "" && ((pac && mode != "pac") || (browser && mode != "system")) {
		conflict = true
	}
	if conflict {
		disableRuntimeProxyForReview(rc)
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{ID: "proxy", Area: "Proxy", Notes: "Browser, PAC, and custom proxy selectors conflict or are malformed. Select one portable proxy mode explicitly; Windows browser configuration must be exported."})
		return
	}
	if pac {
		raw := runtimeSectionValue(web, "ProxyAutoConfigScriptURL")
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			disableRuntimeProxyForReview(rc)
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{ID: "proxy", Area: "Proxy", Notes: "PAC requires an absolute HTTP(S) URL without userinfo."})
			return
		}
		rc.ProxyURL = ""
		rc.ProxyMode = "pac"
		rc.httpCompatibility.Proxy.PACURL = raw
	} else if browser {
		rc.ProxyURL = ""
		rc.ProxyMode = "system"
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{ID: "proxy", Area: "Proxy", Notes: "Portable system mode uses the generator's HTTP_PROXY/HTTPS_PROXY environment. Export Windows browser proxy settings explicitly; desktop inheritance is not claimed."})
	}
	// Contradictory case-insensitive declarations are handled by the shared
	// conflict pass; do not certify ambiguous selectors here.
}
