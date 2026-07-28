package storage

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// LegacyCredentialPreservingImportProvenance reports whether a script carries
// provenance emitted by an importer that pre-dates import_value_policy.
//
// This function classifies provenance; it does not establish trust on its own.
// Callers may use it for canonicalization, but only a script decoded from
// durable server-owned storage may be upgraded to the preservation policy.
func LegacyCredentialPreservingImportProvenance(script *dsl.Script) bool {
	_, ok := LegacyCredentialPreservingImportSource(script)
	return ok
}

// LegacyCredentialPreservingImportSource returns the canonical importer source
// for the bounded compatibility provenance. It remains a classifier only.
func LegacyCredentialPreservingImportSource(script *dsl.Script) (string, bool) {
	if script == nil {
		return "", false
	}
	generatedBy := strings.ToLower(strings.TrimSpace(script.GeneratedBy))
	generatedFrom := strings.ToLower(strings.TrimSpace(script.GeneratedFrom))
	if generatedBy == "" {
		return "", false
	}
	if generatedFrom == "" {
		source, ok := legacySourceLessCredentialImportGenerators[generatedBy]
		return source, ok
	}
	if script.Metadata == nil || script.Metadata.Import == nil {
		return "", false
	}

	allowedSources, ok := legacyCredentialImportProvenance[generatedBy]
	if !ok {
		return "", false
	}
	_, ok = allowedSources[generatedFrom]
	if !ok {
		return "", false
	}
	source := strings.TrimSpace(script.Metadata.Import.Source)
	if source == "" {
		source = generatedFrom
	}
	return source, true
}

// These three recorder identities historically omitted GeneratedFrom entirely.
// They are exact literals emitted by server-owned converters, not a prefix
// match. A non-empty but unknown profile still goes through the paired
// allowlist below and is rejected.
var legacySourceLessCredentialImportGenerators = map[string]string{
	"recorder":                   "recorder",
	"browser-recorder":           "browser_recorder",
	"apexload-recorder-importer": "recorder",
}

var legacyCredentialImportProvenance = map[string]map[string]struct{}{
	"apexload-loadrunner-converter": {
		"loadrunner": {},
	},
	"apexload-devweb-converter": {
		"devweb": {},
	},
	"apexload-asyncapi-converter": {
		"asyncapi": {},
	},
	"sample-import": {
		"raw_http": {}, "curl": {}, "rest_json": {}, "har": {},
	},
	"apexload-har-converter": {
		"har": {},
	},
	"apexload-postman-converter": {
		"postman": {},
	},
	"apexload-openapi-converter": {
		"openapi": {},
	},
	"apexload-wsdl-converter": {
		"wsdl": {},
	},
	"apexload-k6-converter": {
		"k6": {},
	},
	"jmeter-import": {
		"jmeter": {},
	},
	"apexload-bruno-importer": {
		"bru": {}, "yml": {}, "mixed": {},
	},
	"bruno-import": {
		"bru": {}, "yml": {}, "mixed": {},
	},
	"apexload-bruno-native-importer": {
		"open_collection_yaml": {},
	},
	"apexload-enterprise-script-archive-importer": {
		"enterprise_script_archive": {},
	},
	"apexload-enterprise-script-archive-java-importer": {
		"enterprise_script_archive": {},
	},
	"apexload-enterprise-script-archive-sapgui-importer": {
		"enterprise_script_archive": {},
	},
	"apexload-truclient-converter": {
		"enterprise_script_archive": {},
	},
	"apexload-salesforce-builder": {
		"enterprise_script_archive+har": {},
	},
	"apexload-recorder-importer": {
		"recorder": {}, "browser_recording": {}, "browser_recording_har": {},
	},
	"recorder": {
		"recorder": {},
	},
	"browser-recorder": {
		"browser_recording": {}, "browser_recording_har": {},
	},
	"apexload-browser-to-http-converter": {
		"browser_replay": {},
	},
}

// recognizeLegacyCredentialPreservingImport upgrades the in-memory view of an
// importer-produced script written before import_value_policy existed.
//
// The recognition is deliberately applied only after decoding a durable
// server-owned row. A submitted script cannot establish this trust marker:
// ordinary API persistence strips forged import attestations and the current
// import workflows stamp the policy themselves. The bounded GeneratedBy /
// GeneratedFrom pairs cover legacy importer output without trusting the
// caller-controlled metadata source label, which was absent or mismatched in
// several historical import paths.
func recognizeLegacyCredentialPreservingImport(script *dsl.Script) {
	source, ok := LegacyCredentialPreservingImportSource(script)
	if !ok {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: source}
	} else if strings.TrimSpace(script.Metadata.Import.Source) == "" {
		script.Metadata.Import.Source = source
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	if policy, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey].(string); strings.EqualFold(strings.TrimSpace(policy), dsl.ImportPreservedCredentialPolicyPreserve) {
		return
	}
	script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey] =
		dsl.ImportPreservedCredentialPolicyPreserve
}
