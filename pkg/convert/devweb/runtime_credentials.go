package devweb

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

type devWebRuntimeCredentialField struct {
	key      string
	label    string
	context  string
	nameHint string
	pointer  string
	target   string
	value    *string
}

// projectDevWebRuntimeCredentials handles credential-bearing rts.yml values
// that do not belong to an individual WebRequest. Genuine source values remain
// byte-faithful and executable under the import-preserve runtime policy. A
// disclosure sentinel is evidence of an unavailable value, so it becomes an
// unconfigured secret reference and a durable execution gate.
func projectDevWebRuntimeCredentials(
	script *dsl.Script,
	runtimePath string,
	registry *credentialpolicy.NameRegistry,
) ([]credentialpolicy.Requirement, []importreview.Finding, error) {
	if script == nil {
		return nil, nil, nil
	}
	if registry == nil {
		registry = credentialpolicy.NewNameRegistry()
	}
	fields := []devWebRuntimeCredentialField{
		{
			key:      "proxy_url",
			label:    "DevWeb HTTP proxy URL",
			context:  "HTTP proxy URL from rts.yml",
			nameHint: "DEVWEB_PROXY_URL",
			pointer:  "/proxy/proxyServer",
			target:   "config.http.proxy.url",
			value:    &script.Config.HTTP.Proxy.URL,
		},
		{
			key:      "proxy_username",
			label:    "DevWeb HTTP proxy username",
			context:  "HTTP proxy username from rts.yml",
			nameHint: "DEVWEB_PROXY_USERNAME",
			pointer:  "/proxy/proxyUser",
			target:   "config.http.proxy.username",
			value:    &script.Config.HTTP.Proxy.Username,
		},
		{
			key:      "proxy_password",
			label:    "DevWeb HTTP proxy password",
			context:  "HTTP proxy password from rts.yml",
			nameHint: "DEVWEB_PROXY_PASSWORD",
			pointer:  "/proxy/proxyPassword",
			target:   "config.http.proxy.password",
			value:    &script.Config.HTTP.Proxy.Password,
		},
	}
	var requirements []credentialpolicy.Requirement
	var findings []importreview.Finding
	for _, field := range fields {
		value := strings.TrimSpace(*field.value)
		if value == "" {
			continue
		}
		status := credentialpolicy.ClassifySourceCredentialValue(*field.value)
		if status != credentialpolicy.SourceCredentialValueRedactedPlaceholder &&
			status != credentialpolicy.SourceCredentialValueUnsafeEncoding {
			continue
		}
		source := credentialpolicy.SourceRef{
			Format:  "devweb",
			Path:    runtimePath,
			Pointer: field.pointer,
		}
		requirement, replacement, err := credentialpolicy.NewRequirement(
			credentialpolicy.RequirementSpec{
				Key:      field.key,
				Label:    field.label,
				Context:  field.context,
				NameHint: field.nameHint,
				Source:   source,
				Targets: []credentialpolicy.TargetRef{{
					RequestKey: "devweb_runtime",
					Field:      field.target,
				}},
				Value:    *field.value,
				Mode:     credentialpolicy.Preserve,
				Registry: registry,
			},
		)
		if err != nil {
			return nil, nil, fmt.Errorf("project %s: %w", field.target, err)
		}
		*field.value = replacement
		requirements = append(requirements, requirement)
		findings = append(findings, importreview.Finding{
			Code:       "import_credential_source_unavailable",
			Severity:   diag.Blocker,
			Element:    field.label,
			Detail:     "The selected DevWeb runtime source contains a disclosure placeholder instead of recoverable credential material. Configure the generated secret before execution.",
			Location:   runtimePath + "#" + field.pointer,
			Source:     "devweb",
			ReasonCode: "import_credential_source_unavailable",
			SourceRef:  runtimePath + "#" + field.pointer,
			Resolution: "configure_generated_secret",
			Gate:       importreview.GateExecution,
		})
	}
	return requirements, importreview.NormalizeFindings(findings), nil
}

func devWebRuntimeCredentialReviewValues(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	config := script.Config.HTTP
	values := make([]string, 0, 6)
	if value := strings.TrimSpace(config.Proxy.URL); value != "" &&
		scrub.ValueURL(value, scrub.Policy{}) != value {
		values = append(values, value)
	}
	for _, value := range []string{
		config.Proxy.Username,
		config.Proxy.Password,
		config.Auth.Username,
		config.Auth.Password,
		config.Auth.Token,
	} {
		if strings.TrimSpace(value) != "" {
			values = append(values, value)
		}
	}
	return values
}
