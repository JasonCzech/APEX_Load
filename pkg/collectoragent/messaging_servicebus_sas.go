package collectoragent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The literal key and lower-case URL-escaped audience match the pinned
// azservicebus v1.10.0 internal/sas SignWithExpiry implementation. Only an
// approved entity resource URI without the API query can reach this helper.
func serviceBusSASAuthorization(credentials ManagementSharedKeyCredentials, resourceURI string, now, expiresAt time.Time) (string, error) {
	if !serviceBusPolicyName(credentials.KeyName) || credentials.Key == "" || len(credentials.Key) > 8192 {
		return "", &managementObservationError{reason: "permission_denied"}
	}
	u, err := url.Parse(resourceURI)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(resourceURI, "#") || u.RawPath != "" || u.Path == "" || u.Path == "/" {
		return "", &managementObservationError{reason: "permission_denied"}
	}
	origin, host, err := canonicalServiceBusOrigin("https://" + u.Host)
	if err != nil || u.Host != host || resourceURI != origin+u.Path || !managementLabel(resourceURI, 2048) {
		return "", &managementObservationError{reason: "permission_denied"}
	}
	if !serviceBusCanonicalResourcePath(u.Path) {
		return "", &managementObservationError{reason: "permission_denied"}
	}
	if now.IsZero() || expiresAt.IsZero() {
		return "", &managementObservationError{reason: "permission_denied"}
	}
	expiresAt = minTime(expiresAt, now.Add(30*time.Second))
	seconds := expiresAt.Unix() // Floor instead of extending signed authority.
	expiry := time.Unix(seconds, 0)
	if seconds <= 0 || expiry.Sub(now) < time.Second {
		return "", &managementObservationError{reason: "timeout"}
	}
	audience := strings.ToLower(url.QueryEscape(resourceURI))
	text := audience + "\n" + strconv.FormatInt(seconds, 10)
	mac := hmac.New(sha256.New, []byte(credentials.Key))
	_, _ = mac.Write([]byte(text))
	signature := mac.Sum(nil)
	defer clear(signature)
	return "SharedAccessSignature sr=" + audience + "&sig=" + url.QueryEscape(base64.StdEncoding.EncodeToString(signature)) + "&se=" + strconv.FormatInt(seconds, 10) + "&skn=" + url.QueryEscape(credentials.KeyName), nil
}

// Resource paths must have exactly the same unambiguous grammar as the frozen
// allowlist, including the fixed provider subscription separator.
func serviceBusCanonicalResourcePath(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		if !strings.EqualFold(part, "subscriptions") {
			continue
		}
		if part != "Subscriptions" || i < 1 || i != len(parts)-2 {
			return false
		}
		topic, err := serviceBusEntityPath(strings.Join(parts[:i], "/"), false)
		if err != nil {
			return false
		}
		sub, err := serviceBusEntityPath(parts[i+1], true)
		return err == nil && path == "/"+topic+"/Subscriptions/"+sub
	}
	queue, err := serviceBusEntityPath(strings.TrimPrefix(path, "/"), false)
	return err == nil && path == "/"+queue
}
