package api

// Native protocol runtime-override sections.
//
// The kafka, jms, eventhubs, grpc, database, network, appium and java_http
// sections share one shape: RuntimeSettingsOverride carries a pointer to the
// same dsl type ScriptConfig holds by value, under the same JSON key. Without
// override_fields a non-nil pointer replaces the script's whole section (v1);
// with override_fields the reflective partial path addresses their leaves. The
// helpers below wire every section through clone/apply/merge/projection by that
// shared key, so a section can never be wired into one path and missed by
// another.
//
// Their credential leaves follow the http/soap lifecycle: protected into
// controller-owned {{secret.*}} references for execution, masked on every read,
// reconciled against the stored row when a client echoes the mask, kept out of
// apply-to-groups copies, and required to be references in execution plans.
// The secret-bearing leaves are the catalog Secret paths
// (config.kafka.sasl_password, config.kafka.oauth.client_secret,
// config.jms.password, config.eventhubs.connection_string,
// config.database.dsn, config.network.password) plus the shapes the run-side
// credential collectors treat as credentials: usernames beside an active
// credential, credential-bearing URLs, and credential-named or
// credential-shaped property, metadata and capability values.

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// runtimeProtocolOverrideSections lists the native protocol sections in a
// stable order. Each name is the JSON key on both RuntimeSettingsOverride and
// dsl.ScriptConfig, the override_fields top section and the apply-to-groups
// field key. PARITY: pkg/dsl scenarioOverridableConfigSections.
var runtimeProtocolOverrideSections = []string{"kafka", "jms", "eventhubs", "grpc", "database", "network", "appium", "java_http"}

type runtimeProtocolSectionIndex struct {
	override []int
	config   []int
}

var runtimeProtocolSectionFields = func() map[string]runtimeProtocolSectionIndex {
	out := make(map[string]runtimeProtocolSectionIndex, len(runtimeProtocolOverrideSections))
	overrideType := reflect.TypeOf(RuntimeSettingsOverride{})
	configType := reflect.TypeOf(dsl.ScriptConfig{})
	for _, key := range runtimeProtocolOverrideSections {
		overrideField, ok := findStructFieldByJSONTag(overrideType, key)
		if !ok {
			panic("RuntimeSettingsOverride has no " + key + " section")
		}
		configField, ok := findStructFieldByJSONTag(configType, key)
		if !ok {
			panic("dsl.ScriptConfig has no " + key + " section")
		}
		if overrideField.Type.Kind() != reflect.Pointer || overrideField.Type.Elem() != configField.Type {
			panic("RuntimeSettingsOverride." + overrideField.Name + " must be *" + configField.Type.String())
		}
		out[key] = runtimeProtocolSectionIndex{override: overrideField.Index, config: configField.Index}
	}
	return out
}()

func isRuntimeProtocolOverrideSection(key string) bool {
	_, ok := runtimeProtocolSectionFields[key]
	return ok
}

// runtimeProtocolOverrideField returns the settable pointer field of o.
func runtimeProtocolOverrideField(o *RuntimeSettingsOverride, key string) reflect.Value {
	return reflect.ValueOf(o).Elem().FieldByIndex(runtimeProtocolSectionFields[key].override)
}

// runtimeProtocolConfigField returns the settable value field of cfg.
func runtimeProtocolConfigField(cfg *dsl.ScriptConfig, key string) reflect.Value {
	return reflect.ValueOf(cfg).Elem().FieldByIndex(runtimeProtocolSectionFields[key].config)
}

func runtimeProtocolSectionPresent(o *RuntimeSettingsOverride, key string) bool {
	return o != nil && !runtimeProtocolOverrideField(o, key).IsNil()
}

// runtimeProtocolSectionValue is the section as an untyped value for change
// detection: nil when absent, the typed pointer otherwise.
func runtimeProtocolSectionValue(o *RuntimeSettingsOverride, key string) any {
	if !runtimeProtocolSectionPresent(o, key) {
		return nil
	}
	return runtimeProtocolOverrideField(o, key).Interface()
}

// cloneRuntimeProtocolSections deep-copies every native protocol section of
// src into dst (absent sections become nil).
func cloneRuntimeProtocolSections(dst, src *RuntimeSettingsOverride) {
	for _, key := range runtimeProtocolOverrideSections {
		deepCopyReflectValue(runtimeProtocolOverrideField(dst, key), runtimeProtocolOverrideField(src, key))
	}
}

// replaceRuntimeProtocolSection copies one section from src to dst, nil
// included (apply-to-groups copy semantics).
func replaceRuntimeProtocolSection(dst, src *RuntimeSettingsOverride, key string) {
	deepCopyReflectValue(runtimeProtocolOverrideField(dst, key), runtimeProtocolOverrideField(src, key))
}

// mergeRuntimeProtocolSections lets each section the group carries replace
// the merged one (v1 group-wins, as http and soap).
func mergeRuntimeProtocolSections(merged, group *RuntimeSettingsOverride) {
	for _, key := range runtimeProtocolOverrideSections {
		if runtimeProtocolSectionPresent(group, key) {
			replaceRuntimeProtocolSection(merged, group, key)
		}
	}
}

// applyRuntimeProtocolSections replaces each script section the override
// carries with a deep copy (v1 whole-section semantics), and is also how
// overrideToScriptConfig projects the sections for the partial path.
func applyRuntimeProtocolSections(cfg *dsl.ScriptConfig, o *RuntimeSettingsOverride) {
	if cfg == nil || o == nil {
		return
	}
	for _, key := range runtimeProtocolOverrideSections {
		src := runtimeProtocolOverrideField(o, key)
		if src.IsNil() {
			continue
		}
		deepCopyReflectValue(runtimeProtocolConfigField(cfg, key), src.Elem())
	}
}

// setRuntimeProtocolSectionFromConfig stores a deep copy of cfg's section as
// the override's section pointer.
func setRuntimeProtocolSectionFromConfig(o *RuntimeSettingsOverride, cfg *dsl.ScriptConfig, key string) {
	section := runtimeProtocolConfigField(cfg, key)
	pointer := reflect.New(section.Type())
	deepCopyReflectValue(pointer.Elem(), section)
	runtimeProtocolOverrideField(o, key).Set(pointer)
}

// runtimeProtocolSectionsFromMergedConfig fills each touched section of a
// partial merge result from the merged ScriptConfig.
func runtimeProtocolSectionsFromMergedConfig(merged *RuntimeSettingsOverride, cfg *dsl.ScriptConfig, touched map[string]bool) {
	for _, key := range runtimeProtocolOverrideSections {
		if touched[key] {
			setRuntimeProtocolSectionFromConfig(merged, cfg, key)
		}
	}
}

// runtimeProtocolSectionsFromScriptConfig projects each non-zero script
// section into the override shape (runtimeSettingsFromScriptConfig).
func runtimeProtocolSectionsFromScriptConfig(projected *RuntimeSettingsOverride, cfg *dsl.ScriptConfig) {
	for _, key := range runtimeProtocolOverrideSections {
		if !runtimeProtocolConfigField(cfg, key).IsZero() {
			setRuntimeProtocolSectionFromConfig(projected, cfg, key)
		}
	}
}

// presentRuntimeProtocolSections lists the sections o carries, in order.
func presentRuntimeProtocolSections(o *RuntimeSettingsOverride) []string {
	var keys []string
	for _, key := range runtimeProtocolOverrideSections {
		if runtimeProtocolSectionPresent(o, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// clearUnrequestedRuntimeProtocolSections drops every section not requested
// (projectRuntimeFields).
func clearUnrequestedRuntimeProtocolSections(o *RuntimeSettingsOverride, requested map[string]bool) {
	for _, key := range runtimeProtocolOverrideSections {
		if !requested[key] {
			runtimeProtocolOverrideField(o, key).Set(reflect.Zero(runtimeProtocolOverrideField(o, key).Type()))
		}
	}
}

// runtimeProtocolFieldApplies reports whether a native protocol section key
// applies to a group running protocol; ok is false for other keys.
func runtimeProtocolFieldApplies(field, protocol string) (applies, ok bool) {
	if !isRuntimeProtocolOverrideSection(field) {
		return false, false
	}
	return dsl.ProtocolFamily(strings.ToLower(strings.TrimSpace(protocol))) == field, true
}

// ---- credential leaves ---------------------------------------------------

type runtimeCredentialKind int

const (
	// runtimeCredentialSecret is always secret material (catalog Secret).
	runtimeCredentialSecret runtimeCredentialKind = iota
	// runtimeCredentialIdentity is a username: secret only beside an active
	// credential (or when credential-shaped), as http.auth.username.
	runtimeCredentialIdentity
	// runtimeCredentialURL is a URL whose userinfo or credential query and
	// fragment values are secret, as http.proxy.url.
	runtimeCredentialURL
	// runtimeCredentialSecretName is a dedicated field that names a secret
	// (a logical name or one exact {{secret.NAME}} reference). It is never a
	// value, so it is not protected; a literal or a controller-reserved name
	// is refused instead.
	runtimeCredentialSecretName
	// runtimeCredentialFreeText is a property or capability value: secret
	// when its key is credential-named or its text carries credential
	// material (JAAS password=, DSN userinfo, SharedAccessKey=, ...).
	runtimeCredentialFreeText
	// runtimeCredentialHeader is a gRPC metadata value, classified like an
	// HTTP default header.
	runtimeCredentialHeader
)

// runtimeCredentialLeaf is one credential-capable string of a native protocol
// section. path is unique within the override and names the leaf in errors
// and in the derived runtime secret name; fieldPath is the override_fields
// path that owns it; location is its place in the override's JSON.
type runtimeCredentialLeaf struct {
	path      string
	fieldPath string
	location  []any
	kind      runtimeCredentialKind
	key       string
	active    bool
	get       func() string
	set       func(string)
	// remove deletes a map entry; nil for struct fields and list elements.
	remove func()
	// inAnyList marks a string inside a capability array, where the engine
	// does not resolve {{secret.*}} references.
	inAnyList bool
	// capability marks an Appium capability value, whose credential-named
	// keys include device-cloud spellings (browserstack.key) that the env
	// key rule does not recognize.
	capability bool
}

func stringLeaf(path string, kind runtimeCredentialKind, value *string, location ...any) runtimeCredentialLeaf {
	return runtimeCredentialLeaf{
		path:      path,
		fieldPath: path,
		location:  location,
		kind:      kind,
		get:       func() string { return *value },
		set:       func(next string) { *value = next },
	}
}

func stringMapLeaves(prefix string, kind runtimeCredentialKind, values map[string]string, location ...any) []runtimeCredentialLeaf {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	leaves := make([]runtimeCredentialLeaf, 0, len(keys))
	for _, key := range keys {
		mapKey := key
		leaves = append(leaves, runtimeCredentialLeaf{
			path:      prefix + "." + mapKey,
			fieldPath: prefix + "." + mapKey,
			location:  append(slices.Clone(location), executionPlanMapKey(mapKey)),
			kind:      kind,
			key:       mapKey,
			get:       func() string { return values[mapKey] },
			set:       func(next string) { values[mapKey] = next },
			remove:    func() { delete(values, mapKey) },
		})
	}
	return leaves
}

// anyMapLeafPath appends one map key to a leaf path. A key that contains a
// path delimiter is quoted (path["lt:options.accessKey"]), so a flat dotted
// capability name and the nested spelling it joins to never share a path and
// therefore never share a derived runtime secret or a reconcile match.
func anyMapLeafPath(path, key string) string {
	if strings.ContainsAny(key, `.[]"\`) {
		return path + "[" + strconv.Quote(key) + "]"
	}
	return path + "." + key
}

// anyMapLeaves walks a map[string]any (Appium capabilities, which nest
// vendor option objects) and returns every string it holds. fieldPath is the
// top-level key, the finest path override_fields can address.
func anyMapLeaves(prefix string, values map[string]any, location ...any) []runtimeCredentialLeaf {
	var leaves []runtimeCredentialLeaf
	var walk func(path, fieldPath string, key string, node any, location []any, set func(any), remove func(), inList bool)
	walk = func(path, fieldPath string, key string, node any, location []any, set func(any), remove func(), inList bool) {
		switch typed := node.(type) {
		case string:
			current := typed
			leaves = append(leaves, runtimeCredentialLeaf{
				path:      path,
				fieldPath: fieldPath,
				location:  location,
				kind:      runtimeCredentialFreeText,
				key:       key,
				get:       func() string { return current },
				set: func(next string) {
					current = next
					set(next)
				},
				remove:     remove,
				inAnyList:  inList,
				capability: true,
			})
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for childKey := range typed {
				keys = append(keys, childKey)
			}
			sort.Strings(keys)
			for _, childKey := range keys {
				mapKey := childKey
				walk(anyMapLeafPath(path, mapKey), fieldPath, mapKey, typed[mapKey], append(slices.Clone(location), executionPlanMapKey(mapKey)),
					func(next any) { typed[mapKey] = next },
					func() { delete(typed, mapKey) }, inList)
			}
		case []any:
			for index := range typed {
				position := index
				walk(path+"."+strconv.Itoa(position), fieldPath, key, typed[position], append(slices.Clone(location), position),
					func(next any) { typed[position] = next },
					nil, true)
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		mapKey := key
		walk(anyMapLeafPath(prefix, mapKey), prefix+"."+mapKey, mapKey, values[mapKey], append(slices.Clone(location), executionPlanMapKey(mapKey)),
			func(next any) { values[mapKey] = next },
			func() { delete(values, mapKey) }, false)
	}
	return leaves
}

// runtimeProtocolCredentialLeaves lists the credential-capable strings of
// o's native protocol sections, bound to o so callers can rewrite them in
// place. Only sections in keys are walked (all when keys is empty).
func runtimeProtocolCredentialLeaves(o *RuntimeSettingsOverride, keys ...string) []runtimeCredentialLeaf {
	if o == nil {
		return nil
	}
	want := func(key string) bool { return len(keys) == 0 || slices.Contains(keys, key) }
	var leaves []runtimeCredentialLeaf
	if k := o.Kafka; k != nil && want("kafka") {
		active := k.SASLMechanism != "" || k.SASLPassword != "" || k.SASLPasswordSecret != ""
		username := stringLeaf("kafka.sasl_username", runtimeCredentialIdentity, &k.SASLUsername, "kafka", "sasl_username")
		username.active = active
		leaves = append(leaves,
			username,
			stringLeaf("kafka.sasl_password", runtimeCredentialSecret, &k.SASLPassword, "kafka", "sasl_password"),
			stringLeaf("kafka.sasl_password_secret", runtimeCredentialSecretName, &k.SASLPasswordSecret, "kafka", "sasl_password_secret"),
			stringLeaf("kafka.schema_registry_url", runtimeCredentialURL, &k.SchemaRegistryURL, "kafka", "schema_registry_url"),
		)
		if k.OAuth != nil {
			leaves = append(leaves,
				stringLeaf("kafka.oauth.client_secret", runtimeCredentialSecret, &k.OAuth.ClientSecret, "kafka", "oauth", "client_secret"),
				stringLeaf("kafka.oauth.client_secret_ref", runtimeCredentialSecretName, &k.OAuth.ClientSecretRef, "kafka", "oauth", "client_secret_ref"),
				// The token endpoint refuses userinfo at validation but
				// allows a query, which can carry a client secret.
				stringLeaf("kafka.oauth.token_url", runtimeCredentialURL, &k.OAuth.TokenURL, "kafka", "oauth", "token_url"),
			)
		}
		for index := range k.Brokers {
			leaf := stringLeaf("kafka.brokers."+strconv.Itoa(index), runtimeCredentialURL, &k.Brokers[index], "kafka", "brokers", index)
			leaf.fieldPath = "kafka.brokers"
			leaves = append(leaves, leaf)
		}
		leaves = append(leaves, stringMapLeaves("kafka.properties", runtimeCredentialFreeText, k.Properties, "kafka", "properties")...)
	}
	if j := o.JMS; j != nil && want("jms") {
		username := stringLeaf("jms.username", runtimeCredentialIdentity, &j.Username, "jms", "username")
		username.active = j.Password != ""
		leaves = append(leaves,
			username,
			stringLeaf("jms.password", runtimeCredentialSecret, &j.Password, "jms", "password"),
			stringLeaf("jms.broker_url", runtimeCredentialURL, &j.BrokerURL, "jms", "broker_url"),
		)
		if j.TLS != nil {
			leaves = append(leaves, stringLeaf("jms.tls.ca_cert_secret", runtimeCredentialSecretName, &j.TLS.CACertSecret, "jms", "tls", "ca_cert_secret"))
		}
		leaves = append(leaves, stringMapLeaves("jms.properties", runtimeCredentialFreeText, j.Properties, "jms", "properties")...)
	}
	if e := o.EventHubs; e != nil && want("eventhubs") {
		leaves = append(leaves,
			stringLeaf("eventhubs.connection_string", runtimeCredentialSecret, &e.ConnectionString, "eventhubs", "connection_string"),
			stringLeaf("eventhubs.connection_string_secret", runtimeCredentialSecretName, &e.ConnectionStringSecret, "eventhubs", "connection_string_secret"),
			stringLeaf("eventhubs.checkpoint_container_url", runtimeCredentialURL, &e.CheckpointContainerURL, "eventhubs", "checkpoint_container_url"),
		)
	}
	if g := o.GRPC; g != nil && want("grpc") {
		leaves = append(leaves, stringMapLeaves("grpc.metadata", runtimeCredentialHeader, g.Metadata, "grpc", "metadata")...)
	}
	if d := o.Database; d != nil && want("database") {
		leaves = append(leaves,
			stringLeaf("database.dsn", runtimeCredentialSecret, &d.DSN, "database", "dsn"),
			stringLeaf("database.dsn_secret", runtimeCredentialSecretName, &d.DSNSecret, "database", "dsn_secret"),
		)
	}
	if n := o.Network; n != nil && want("network") {
		username := stringLeaf("network.username", runtimeCredentialIdentity, &n.Username, "network", "username")
		username.active = n.Password != ""
		leaves = append(leaves,
			username,
			stringLeaf("network.password", runtimeCredentialSecret, &n.Password, "network", "password"),
			stringLeaf("network.address", runtimeCredentialURL, &n.Address, "network", "address"),
		)
	}
	if a := o.Appium; a != nil && want("appium") {
		leaves = append(leaves, stringLeaf("appium.server_url", runtimeCredentialURL, &a.ServerURL, "appium", "server_url"))
		leaves = append(leaves, anyMapLeaves("appium.capabilities", a.Capabilities, "appium", "capabilities")...)
	}
	return leaves
}

// runtimeFreeTextSecretBearing classifies a property, metadata-like or
// capability value. A value that only combines {{secret.*}} references with
// non-credential text (a JAAS template whose password is a reference) is not
// secret material; literal credential material beside a reference is.
// capability selects the Appium capability key rule.
func runtimeFreeTextSecretBearing(key, value string, capability bool) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	sensitiveKey := sensitiveEnvKey(key) || (capability && credentialCapabilityKey(key))
	if runtimeOverrideExactSecretPattern.MatchString(trimmed) {
		return sensitiveKey
	}
	if runtimeOverrideAnySecretPattern.MatchString(value) {
		return importSettingTextCarriesCredential(value)
	}
	return sensitiveKey || sensitiveEnvValue(value) || importSettingTextCarriesCredential(value)
}

// credentialCapabilityTails are the final name parts of the Appium
// capabilities device clouds read credentials from: BrowserStack's legacy
// browserstack.key, Sauce/LambdaTest accessKey, TestObject's
// testobject_api_key, and token, secret and password forms.
var credentialCapabilityTails = map[string]bool{
	"key": true, "accesskey": true, "apikey": true, "token": true, "accesstoken": true,
	"authtoken": true, "secret": true, "password": true, "passwd": true, "pwd": true,
}

// credentialCapabilityKey reports a capability name whose final dotted or
// colon segment (or that segment's last _/- part) names a credential. The
// mirror is cmd/apexctl cliCredentialCapabilityKey.
func credentialCapabilityKey(key string) bool {
	name := strings.ToLower(strings.TrimSpace(key))
	if index := strings.LastIndexAny(name, ".:"); index >= 0 {
		name = name[index+1:]
	}
	if credentialCapabilityTails[name] {
		return true
	}
	if index := strings.LastIndexAny(name, "_-"); index >= 0 {
		return credentialCapabilityTails[name[index+1:]]
	}
	return false
}

// runtimeURLSecretBearing reports a URL with userinfo or a credential query
// or fragment value, including the SAS signature (sig=) shapes the env rule
// recognizes. A URL whose every such credential is one exact non-reserved
// {{secret.NAME}} reference (the documented device-cloud form
// https://{{secret.USER}}:{{secret.KEY}}@hub/...) holds no secret material,
// the same rule the execution-plan check applies; literal material beside a
// reference, or a reserved reference, is still secret-bearing.
func runtimeURLSecretBearing(value string) bool {
	if maskURLSecrets(value) != value {
		return executionPlanURLCarriesTypedCredential(value)
	}
	if runtimeOverrideAnySecretPattern.MatchString(value) {
		return false
	}
	return sensitiveEnvValue(value)
}

// textHasReservedRuntimeSecretReference reports a controller-reserved
// APEX_RUNTIME_OVERRIDE_* reference anywhere in text.
func textHasReservedRuntimeSecretReference(text string) bool {
	for _, match := range runtimeOverrideSecretReferencePattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		if name, err := engine.NormalizeLogicalSecretName(match[1]); err == nil && strings.HasPrefix(name, runtimeOverrideSecretPrefix) {
			return true
		}
	}
	return false
}

// runtimeSecretNameFieldIssue validates a dedicated secret-name field: empty,
// a logical secret name, or one exact {{secret.NAME}} reference, and never a
// controller-reserved name. The engine uses a value that is neither as the
// literal credential, so such a value is refused rather than stored.
func runtimeSecretNameFieldIssue(path, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	name := trimmed
	if match := runtimeOverrideExactSecretPattern.FindStringSubmatch(trimmed); len(match) == 2 {
		name = match[1]
	} else if strings.Contains(trimmed, "{{") || !engine.ValidLogicalSecretName(trimmed) {
		return fmt.Errorf("runtime override %s must name a secret (NAME or {{secret.NAME}}); enter the credential itself in the Credentials store", path)
	}
	normalized, err := engine.NormalizeLogicalSecretName(name)
	if err != nil {
		return fmt.Errorf("runtime override %s must name a secret (NAME or {{secret.NAME}}); enter the credential itself in the Credentials store", path)
	}
	if strings.HasPrefix(normalized, runtimeOverrideSecretPrefix) {
		return fmt.Errorf("runtime override %s cannot name a reserved internal secret; use a configured project secret with a non-reserved name", path)
	}
	return nil
}

// runtimeCredentialLeafSecretBearing reports whether the leaf's current value
// is credential material that the controller turns into a runtime secret.
func runtimeCredentialLeafSecretBearing(leaf runtimeCredentialLeaf) bool {
	value := leaf.get()
	if strings.TrimSpace(value) == "" {
		return false
	}
	switch leaf.kind {
	case runtimeCredentialSecret:
		return true
	case runtimeCredentialIdentity:
		return leaf.active || sensitiveEnvValue(value)
	case runtimeCredentialURL:
		return runtimeURLSecretBearing(value)
	case runtimeCredentialFreeText:
		return runtimeFreeTextSecretBearing(leaf.key, value, leaf.capability)
	case runtimeCredentialHeader:
		return scrub.Policy{}.SensitiveHeader(leaf.key) || sensitiveEnvValue(value)
	}
	return false
}

// protectRuntimeProtocolSecrets moves every secret-bearing leaf into a
// controller-owned reference through protect (the caller's closure, which
// records the value) and refuses literals in secret-name fields.
func protectRuntimeProtocolSecrets(o *RuntimeSettingsOverride, protect func(path, value string, sensitive bool) (string, error)) error {
	for _, leaf := range runtimeProtocolCredentialLeaves(o) {
		value := leaf.get()
		if leaf.kind == runtimeCredentialSecretName {
			if err := runtimeSecretNameFieldIssue(leaf.path, value); err != nil {
				return err
			}
			continue
		}
		sensitive := runtimeCredentialLeafSecretBearing(leaf)
		if sensitive && leaf.inAnyList && !runtimeOverrideExactSecretPattern.MatchString(strings.TrimSpace(value)) {
			// The engine resolves references in capability maps, not in
			// capability lists, so a protected value there would never run.
			return fmt.Errorf("runtime override %s holds a credential inside a list; move it to a named capability", leaf.path)
		}
		next, err := protect(leaf.path, value, sensitive)
		if err != nil {
			return err
		}
		if next != value {
			leaf.set(next)
		}
	}
	return nil
}

// maskRuntimeProtocolSecrets redacts the secret-bearing leaves of a cloned
// override in place, plus any leaf holding a controller-reserved reference
// (the protected form of a value that was credential-shaped). With
// showReferences (the editor projection) a leaf that is one exact project
// secret reference stays readable, so a reopened scenario shows which secret
// it binds; the hash form masks it, as for http.
func maskRuntimeProtocolSecrets(o *RuntimeSettingsOverride, showReferences bool) {
	for _, leaf := range runtimeProtocolCredentialLeaves(o) {
		value := leaf.get()
		if value == "" {
			continue
		}
		if showReferences && leaf.kind != runtimeCredentialURL && isProjectSecretReference(value) {
			continue
		}
		switch leaf.kind {
		case runtimeCredentialURL:
			if masked := maskURLSecrets(value); masked != value {
				leaf.set(masked)
			} else if runtimeURLSecretBearing(value) || textHasReservedRuntimeSecretReference(value) {
				// A credential the URL masker does not place (a SAS sig=
				// value) redacts the whole URL.
				leaf.set(scrub.Redacted)
			}
		case runtimeCredentialSecretName:
			if isReservedRuntimeOverrideSecretReference(value) || runtimeSecretNameFieldIssue(leaf.path, value) != nil {
				leaf.set(scrub.Redacted)
			}
		default:
			if runtimeCredentialLeafSecretBearing(leaf) || textHasReservedRuntimeSecretReference(value) {
				leaf.set(scrub.Redacted)
			}
		}
	}
}

// reconcileRuntimeProtocolSecrets restores leaves a client echoed back as the
// redaction sentinel from the prior stored override at the same path. A
// sentinel with no secret-bearing prior value is dropped (fail closed); list
// elements are matched by position only while the list length is unchanged.
func reconcileRuntimeProtocolSecrets(incoming, prior *RuntimeSettingsOverride) {
	if incoming == nil {
		return
	}
	priorLeaves := map[string]runtimeCredentialLeaf{}
	for _, leaf := range runtimeProtocolCredentialLeaves(prior) {
		priorLeaves[leaf.path] = leaf
	}
	priorBrokers := 0
	if prior != nil && prior.Kafka != nil {
		priorBrokers = len(prior.Kafka.Brokers)
	}
	droppedBroker := false
	for _, leaf := range runtimeProtocolCredentialLeaves(incoming) {
		value := leaf.get()
		priorLeaf, hasPrior := priorLeaves[leaf.path]
		priorValue := ""
		if hasPrior {
			priorValue = priorLeaf.get()
		}
		isBroker := leaf.fieldPath == "kafka.brokers"
		if isBroker && incoming.Kafka != nil && len(incoming.Kafka.Brokers) != priorBrokers {
			// Positions moved, so a sentinel cannot be matched safely.
			hasPrior, priorValue = false, ""
		}
		switch leaf.kind {
		case runtimeCredentialSecret, runtimeCredentialIdentity:
			if value == scrub.Redacted {
				leaf.set(priorValue)
			}
		case runtimeCredentialSecretName:
			if value == scrub.Redacted {
				leaf.set(priorValue)
			}
		case runtimeCredentialURL:
			if !strings.Contains(value, scrub.Redacted) && !isMaskedURLUserinfo(value) {
				continue
			}
			if isBroker && !hasPrior {
				leaf.set("")
				droppedBroker = true
				continue
			}
			leaf.set(reconcileMaskedURL(value, priorValue))
		case runtimeCredentialFreeText, runtimeCredentialHeader:
			priorSecret := hasPrior && (runtimeCredentialLeafSecretBearing(priorLeaf) || textHasReservedRuntimeSecretReference(priorValue))
			switch {
			case value == scrub.Redacted && priorSecret:
				leaf.set(priorValue)
			case value == scrub.Redacted:
				if leaf.remove != nil {
					leaf.remove()
				} else {
					leaf.set("")
				}
			case value == "" && priorSecret && leaf.remove != nil:
				leaf.remove()
			}
		}
	}
	if droppedBroker {
		incoming.Kafka.Brokers = slices.DeleteFunc(incoming.Kafka.Brokers, func(broker string) bool { return broker == "" })
	}
}

// runtimeProtocolCredentialFieldPaths lists the override_fields paths of the
// secret-bearing leaves (and secret-name fields) a section of o holds, which
// apply-to-groups never copies between groups.
func runtimeProtocolCredentialFieldPaths(o *RuntimeSettingsOverride, section string) []string {
	var paths []string
	for _, leaf := range runtimeProtocolCredentialLeaves(o, section) {
		value := leaf.get()
		if strings.TrimSpace(value) == "" {
			continue
		}
		credential := runtimeCredentialLeafSecretBearing(leaf) || textHasReservedRuntimeSecretReference(value) ||
			isReservedRuntimeOverrideSecretReference(value)
		if leaf.kind == runtimeCredentialSecretName && runtimeSecretNameFieldIssue(leaf.path, value) != nil {
			// A script's secret-name field holding a literal is the literal
			// credential to the engine; copying it would store the source
			// script's credential in the target row.
			credential = true
		}
		if leaf.kind == runtimeCredentialIdentity && !leaf.active {
			// An identity beside no credential is copied like any setting,
			// unless it is itself credential-shaped.
			credential = sensitiveEnvValue(value) || isReservedRuntimeOverrideSecretReference(value)
		}
		if credential && !slices.Contains(paths, leaf.fieldPath) {
			paths = append(paths, leaf.fieldPath)
		}
	}
	return paths
}

// runtimeConfigPathHasValue reports whether cfg holds a non-zero value at a
// canonical override_fields path.
func runtimeConfigPathHasValue(cfg dsl.ScriptConfig, path string) bool {
	var probe dsl.ScriptConfig
	if err := applyConfigOverrideField(&probe, &cfg, path); err != nil {
		return false
	}
	var empty dsl.ScriptConfig
	_ = applyConfigOverrideField(&empty, &dsl.ScriptConfig{}, path)
	return !reflect.DeepEqual(probe, empty)
}

// keepTargetProtocolCredentials keeps a native protocol section's credential
// leaves out of an apply-to-groups copy, as keepTargetSOAPCredentials does
// for WS-Security: copying them would store the source script's literal in
// the target row or hand the target the source group's controller-owned
// reference. A credential the target's own prior override set is kept; every
// other credential leaf is left out of the row, whose override_fields then
// cover the copied section except those leaves, so the target keeps
// inheriting them from its script or the scenario root.
//
// The copy still replaces every other value: a map that keeps a credential key
// out is listed key by key, and each key only the target's inherited layers
// (inheritedTargets: its script and the scenario root) hold is listed too, so
// it is cleared exactly as the whole-section copy clears it.
func keepTargetProtocolCredentials(next, prior *RuntimeSettingsOverride, section string, inheritedTargets ...*RuntimeSettingsOverride) *RuntimeSettingsOverride {
	if !runtimeProtocolSectionPresent(next, section) {
		return next
	}
	paths := runtimeProtocolCredentialFieldPaths(next, section)
	if len(paths) == 0 {
		return next
	}
	nextCfg := overrideToScriptConfig(next)
	priorFields := resolveEffectiveOverrideFields(prior)
	priorCfg := overrideToScriptConfig(prior)
	priorOwns := runtimeProtocolSectionPresent(prior, section)
	var empty dsl.ScriptConfig
	inherited := map[string]bool{}
	for _, path := range paths {
		if priorOwns && overrideFieldsCoverPath(priorFields, path) && runtimeConfigPathHasValue(priorCfg, path) {
			_ = applyConfigOverrideField(&nextCfg, &priorCfg, path)
			continue
		}
		_ = applyConfigOverrideField(&nextCfg, &empty, path)
		inherited[path] = true
	}
	setRuntimeProtocolSectionFromConfig(next, &nextCfg, section)
	if len(inherited) == 0 {
		return next
	}
	if len(next.OverrideFields) == 0 {
		dropV1ShadowedRuntimeAliases(next)
	}
	fields := make([]string, 0, 8)
	for _, path := range resolveEffectiveOverrideFields(next) {
		if overrideFieldTopSection(path) != section {
			fields = append(fields, path)
		}
	}
	targets := make([]reflect.Value, 0, len(inheritedTargets)+1)
	for _, target := range append([]*RuntimeSettingsOverride{prior}, inheritedTargets...) {
		if runtimeProtocolSectionPresent(target, section) {
			targets = append(targets, runtimeProtocolOverrideField(target, section))
		}
	}
	next.OverrideFields = append(fields, overrideFieldPathsExcludingValue(runtimeProtocolConfigField(&nextCfg, section), section, inherited, targets...)...)
	return next
}

// runtimeProtocolUncopyableCredentialPaths lists the credential paths of a
// section that apply-to-groups cannot leave out without changing what the
// copy means: a credential inside a list (a Kafka broker with userinfo) would
// drop the whole list, so the target would silently keep its own brokers.
func runtimeProtocolUncopyableCredentialPaths(o *RuntimeSettingsOverride, section string) []string {
	credentialPaths := runtimeProtocolCredentialFieldPaths(o, section)
	var paths []string
	for _, leaf := range runtimeProtocolCredentialLeaves(o, section) {
		if len(leaf.location) == 0 || !slices.Contains(credentialPaths, leaf.fieldPath) || slices.Contains(paths, leaf.fieldPath) {
			continue
		}
		if _, listElement := leaf.location[len(leaf.location)-1].(int); listElement && leaf.fieldPath != leaf.path {
			paths = append(paths, leaf.fieldPath)
		}
	}
	return paths
}

// overrideFieldPathsExcludingValue is overrideFieldPathsExcluding for a
// concrete section value: a map with an excluded key is expanded into one
// path per remaining key it or any of targets (the same section in the
// layers the copy replaces) holds.
func overrideFieldPathsExcludingValue(value reflect.Value, prefix string, excluded map[string]bool, targets ...reflect.Value) []string {
	value = derefReflectValue(value)
	for index := range targets {
		targets[index] = derefReflectValue(targets[index])
	}
	t := value.Type()
	var paths []string
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := prefix + "." + name
		if excluded[path] {
			continue
		}
		expand := false
		for leaf := range excluded {
			if strings.HasPrefix(leaf, path+".") {
				expand = true
				break
			}
		}
		if !expand {
			paths = append(paths, path)
			continue
		}
		field := derefReflectValue(value.Field(i))
		targetFields := make([]reflect.Value, 0, len(targets))
		for _, target := range targets {
			if target.IsValid() && target.Type() == t {
				targetFields = append(targetFields, target.Field(i))
			}
		}
		if field.Kind() == reflect.Map {
			seen := map[string]bool{}
			var keys []string
			for _, source := range append([]reflect.Value{field}, targetFields...) {
				source = derefReflectValue(source)
				if source.Kind() != reflect.Map {
					continue
				}
				for _, key := range source.MapKeys() {
					if name := key.String(); !seen[name] {
						seen[name] = true
						keys = append(keys, name)
					}
				}
			}
			sort.Strings(keys)
			for _, key := range keys {
				if keyPath := path + "." + key; !excluded[keyPath] {
					paths = append(paths, keyPath)
				}
			}
			continue
		}
		paths = append(paths, overrideFieldPathsExcludingValue(value.Field(i), path, excluded, targetFields...)...)
	}
	return paths
}

// derefReflectValue follows pointers, using the zero element for nil.
func derefReflectValue(value reflect.Value) reflect.Value {
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reflect.Zero(value.Type().Elem())
		}
		value = value.Elem()
	}
	return value
}

// referenceRuntimeOverrideSecretNames makes every secret-name field an
// override supplied resolve only as a secret reference: a bare logical name
// becomes {{secret.NAME}} in the applied config. The engine otherwise uses a
// bare name that names no project secret as the credential text itself, so a
// password typed into a name field would authenticate. Script-sourced values
// are untouched; only the fields the override owns (its whole section under
// v1, the listed leaves with override_fields) are rewritten.
func referenceRuntimeOverrideSecretNames(cfg *dsl.ScriptConfig, overrides *RuntimeSettingsOverride) {
	if cfg == nil || overrides == nil {
		return
	}
	var fields []string
	if len(overrides.OverrideFields) > 0 {
		normalized, err := normalizeRuntimeOverrideFields(overrides.OverrideFields)
		if err != nil {
			return
		}
		fields = normalized
	}
	owned := func(leaf runtimeCredentialLeaf) bool {
		if len(overrides.OverrideFields) == 0 {
			return true
		}
		return overrideFieldsCoverPath(fields, leaf.fieldPath)
	}
	ownedNames := map[string]bool{}
	for _, leaf := range runtimeProtocolCredentialLeaves(overrides) {
		if leaf.kind == runtimeCredentialSecretName && strings.TrimSpace(leaf.get()) != "" && owned(leaf) {
			ownedNames[leaf.path] = true
		}
	}
	if len(ownedNames) == 0 {
		return
	}
	// Bind leaves to cfg's own sections so they rewrite it in place.
	view := &RuntimeSettingsOverride{}
	for _, key := range runtimeProtocolOverrideSections {
		runtimeProtocolOverrideField(view, key).Set(runtimeProtocolConfigField(cfg, key).Addr())
	}
	for _, leaf := range runtimeProtocolCredentialLeaves(view) {
		if !ownedNames[leaf.path] || leaf.kind != runtimeCredentialSecretName {
			continue
		}
		name := strings.TrimSpace(leaf.get())
		if name != "" && !strings.Contains(name, "{{") && engine.ValidLogicalSecretName(name) {
			leaf.set("{{secret." + name + "}}")
		}
	}
}

// executionPlanProtocolCredentials adds the native protocol leaves of a plan
// entry row that hold a typed credential (or a literal in a secret-name
// field) instead of a project credential reference.
func executionPlanProtocolCredentials(overrides *RuntimeSettingsOverride, add func(path, value string, url bool, location ...any)) {
	for _, leaf := range runtimeProtocolCredentialLeaves(overrides) {
		value := leaf.get()
		if strings.TrimSpace(value) == "" {
			continue
		}
		switch leaf.kind {
		case runtimeCredentialSecret:
			if !isExecutionPlanSecretReference(value) {
				add(leaf.path, value, false, leaf.location...)
			}
		case runtimeCredentialURL:
			typedSignature := sensitiveEnvValue(value) && !runtimeOverrideAnySecretPattern.MatchString(value)
			if executionPlanURLCarriesTypedCredential(value) || typedSignature || textHasReservedRuntimeSecretReference(value) {
				// Mask component-wise only where the URL masker places the
				// credential; otherwise redact the whole value.
				add(leaf.path, value, maskURLSecrets(value) != value, leaf.location...)
			}
		case runtimeCredentialSecretName:
			if runtimeSecretNameFieldIssue(leaf.path, value) != nil {
				add(leaf.path, value, false, leaf.location...)
			}
		case runtimeCredentialHeader:
			if executionPlanHeaderNeedsReference(leaf.key, value) || textHasReservedRuntimeSecretReference(value) {
				add(leaf.path, value, false, leaf.location...)
			}
		case runtimeCredentialFreeText:
			if textHasReservedRuntimeSecretReference(value) ||
				(!isExecutionPlanSchemedSecretReference(value) && runtimeFreeTextSecretBearing(leaf.key, value, leaf.capability)) {
				add(leaf.path, value, false, leaf.location...)
			}
		}
	}
}
