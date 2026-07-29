package storage

import (
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/scrub"
)

var importBatchRuntimeTemplatePattern = regexp.MustCompile(`\{\{[^{}\r\n]{1,256}\}\}`)

const (
	maxTrustedImportSourceEnvelopeItems = 256
	maxTrustedImportSourceValueBytes    = 64 << 10
	maxTrustedImportSourceEnvelopeBytes = 2 << 20
)

type importBatchCredentialContext struct {
	dynamicReadiness requestimport.VariableReadiness
	actionPath       string
}

func newImportBatchCredentialContext(script *dsl.Script) importBatchCredentialContext {
	if script == nil {
		return importBatchCredentialContext{}
	}
	// Shared missing-variable readiness intentionally treats script parameters
	// as available. Credential provenance is stricter: remove parameters from a
	// shallow analysis copy so only a prior extractor or OAuth token action can
	// satisfy {{var.*}} on the current execution path.
	dynamicOnly := *script
	dynamicOnly.Parameters = nil
	return importBatchCredentialContext{
		dynamicReadiness: requestimport.AnalyzeVariableReadiness(&dynamicOnly, nil),
	}
}

func (context importBatchCredentialContext) forAction(path string) importBatchCredentialContext {
	context.actionPath = strings.TrimSpace(path)
	return context
}

func (context importBatchCredentialContext) provesDynamicVariable(name string) bool {
	name = strings.TrimSpace(name)
	if context.actionPath == "" || !importBatchDynamicVariableName(name) {
		return false
	}
	for _, missing := range context.dynamicReadiness.ByAction[context.actionPath] {
		if missing == name {
			return false
		}
	}
	return true
}

// validateImportBatchCredentialSafety is the storage-side fail-closed boundary
// for generic atomic import commits. Import adapters normally project source
// credentials before this point; this check prevents a future or internal
// caller from persisting an unprojected executable value in either the live
// script rows or the immutable receipt snapshots.
//
// This function only returns structural paths and a stable sentinel. It must
// never include the rejected value in an error.
func validateImportBatchCredentialSafety(
	script *dsl.Script,
	trustPreservedExecutableCredentials bool,
) error {
	if script == nil {
		return ErrImportBatchCredentialsUnmanaged
	}
	context := newImportBatchCredentialContext(script)
	if script.Metadata != nil {
		metadata, err := importBatchJSONValue(script.Metadata)
		if err != nil {
			return unmanagedImportBatchCredential("metadata")
		}
		metadata, err = importBatchMetadataForCredentialValidation(
			metadata,
			trustPreservedExecutableCredentials &&
				importBatchPreservesExecutableCredentials(script),
		)
		if err != nil {
			return err
		}
		if err := validateImportBatchMetadataCredentials(metadata, "metadata", context, false); err != nil {
			return err
		}
	}
	if trustPreservedExecutableCredentials && importBatchPreservesExecutableCredentials(script) {
		// The server-owned import finalizer marks exact runnable imports after
		// review evidence has been sanitized. Their executable values are
		// intentionally retained: the operator already supplied the source
		// credentials, and atomic persistence must not convert them into missing
		// bindings. Metadata remains subject to the disclosure check above.
		return nil
	}
	if err := validateImportBatchRootCredentials(script, context); err != nil {
		return err
	}
	if err := validateImportBatchConfigCredentials(script, context); err != nil {
		return err
	}
	return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		actionContext := context.forAction(ref.Path)
		switch {
		case ref.Action != nil:
			return validateImportBatchActionCredentials(ref.Action, ref.Path, actionContext)
		case ref.Request != nil:
			payload, err := json.Marshal(ref.Request)
			if err != nil {
				return unmanagedImportBatchCredential(ref.Path)
			}
			var action dsl.Action
			if err := json.Unmarshal(payload, &action); err != nil {
				return unmanagedImportBatchCredential(ref.Path)
			}
			return validateImportBatchActionCredentials(&action, ref.Path, actionContext)
		default:
			return nil
		}
	})
}

func importBatchMetadataForCredentialValidation(
	value any,
	trustServerSourceEnvelopes bool,
) (any, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	importMetadata, ok := root["import"].(map[string]any)
	if !ok {
		return value, nil
	}
	preserved, ok := importMetadata["preserved"].(map[string]any)
	if !ok {
		return value, nil
	}
	for _, entry := range []struct {
		key      string
		validate func(any) bool
	}{
		{
			key:      dsl.ImportPreservedEnterpriseJavaVMSourceKey,
			validate: validTrustedJavaVMSourceEnvelope,
		},
		{
			key:      dsl.ImportPreservedEnterpriseJavaJDBCSourceKey,
			validate: validTrustedJavaJDBCSourceEnvelope,
		},
	} {
		envelope, exists := preserved[entry.key]
		if !exists {
			continue
		}
		if !trustServerSourceEnvelopes || !entry.validate(envelope) {
			return nil, unmanagedImportBatchCredential(
				"metadata.import.preserved." + entry.key,
			)
		}
		// value is a detached JSON audit copy. Removing a validated server-owned
		// inert envelope here does not alter the durable executable script; it only
		// prevents the generic metadata rule from treating repair material as an
		// unmanaged outbound credential.
		delete(preserved, entry.key)
	}
	return value, nil
}

func validTrustedJavaVMSourceEnvelope(value any) bool {
	envelope, ok := value.(map[string]any)
	if !ok || !validTrustedImportEnvelopeKeys(
		envelope,
		"version",
		"rows_base64",
	) || !trustedImportEnvelopeVersionOne(envelope["version"]) {
		return false
	}
	rows, ok := envelope["rows_base64"].([]any)
	if !ok || len(rows) == 0 || len(rows) > maxTrustedImportSourceEnvelopeItems {
		return false
	}
	total := 0
	for _, raw := range rows {
		encoded, ok := raw.(string)
		if !ok || !validTrustedImportSourceBase64(encoded, &total) {
			return false
		}
	}
	return total <= maxTrustedImportSourceEnvelopeBytes
}

func validTrustedJavaJDBCSourceEnvelope(value any) bool {
	envelope, ok := value.(map[string]any)
	if !ok || !validTrustedImportEnvelopeKeys(
		envelope,
		"version",
		"connections",
	) || !trustedImportEnvelopeVersionOne(envelope["version"]) {
		return false
	}
	connections, ok := envelope["connections"].([]any)
	if !ok || len(connections) == 0 ||
		len(connections) > maxTrustedImportSourceEnvelopeItems {
		return false
	}
	total := 0
	for _, raw := range connections {
		connection, ok := raw.(map[string]any)
		if !ok || !validTrustedImportEnvelopeKeys(
			connection,
			"driver_base64",
			"dsn_base64",
			"reason",
			"source_ref",
		) {
			return false
		}
		for _, key := range []string{"driver_base64", "dsn_base64"} {
			encoded, ok := connection[key].(string)
			if !ok || (key == "dsn_base64" && encoded == "") ||
				!validTrustedImportSourceBase64(encoded, &total) {
				return false
			}
		}
		for _, key := range []string{"reason", "source_ref"} {
			text, ok := connection[key].(string)
			if !ok || len(text) > 4096 || importBatchContainsControl(text) {
				return false
			}
			total += len(text)
		}
	}
	return total <= maxTrustedImportSourceEnvelopeBytes
}

func validTrustedImportEnvelopeKeys(value map[string]any, allowed ...string) bool {
	if len(value) != len(allowed) {
		return false
	}
	for _, key := range allowed {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func trustedImportEnvelopeVersionOne(value any) bool {
	number, ok := value.(float64)
	return ok && number == 1
}

func validTrustedImportSourceBase64(encoded string, total *int) bool {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxTrustedImportSourceValueBytes) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) > maxTrustedImportSourceValueBytes {
		return false
	}
	*total += len(decoded)
	clear(decoded)
	return *total <= maxTrustedImportSourceEnvelopeBytes
}

func importBatchContainsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func importBatchPreservesExecutableCredentials(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		strings.TrimSpace(script.Metadata.Import.Source) == "" {
		return false
	}
	policy, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey].(string)
	return strings.EqualFold(
		strings.TrimSpace(policy),
		dsl.ImportPreservedCredentialPolicyPreserve,
	)
}

func validateImportBatchRootCredentials(script *dsl.Script, context importBatchCredentialContext) error {
	for index := range script.Parameters {
		parameter := &script.Parameters[index]
		if !importBatchSensitiveBindingName(parameter.Name) &&
			!importBatchSensitiveBindingName(parameter.Column) {
			continue
		}
		// Parameter values are static import material. Even a template in this
		// list must be secret- or environment-governed; param/data/var
		// indirection cannot manufacture dynamic provenance.
		for valueIndex, value := range parameter.Values {
			if err := validateImportBatchCredentialScalar(
				value,
				false,
				fmt.Sprintf("parameters[%d].values[%d]", index, valueIndex),
				context,
			); err != nil {
				return err
			}
		}
	}
	for index := range script.DataSources {
		if err := validateImportBatchDataSourceCredentials(
			script.DataSources[index],
			fmt.Sprintf("data_sources[%d]", index),
			context,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateImportBatchDataSourceCredentials(
	source dsl.DataSource,
	path string,
	context importBatchCredentialContext,
) error {
	if strings.TrimSpace(source.InlineData) == "" {
		return nil
	}
	sourceType := strings.ToLower(strings.TrimSpace(source.Type))
	if sourceType == "file" {
		return validateImportBatchFileDataSourceCredentials(source, path, context)
	}
	if sourceType != "csv" && sourceType != "dat" {
		return nil
	}
	limits := credentialpolicy.DefaultLimits()
	if len(source.InlineData) > limits.MaxBytes {
		return unmanagedImportBatchCredential(path)
	}
	reader := csv.NewReader(strings.NewReader(source.InlineData))
	reader.Comma = dsl.ResolveDataSourceDelimiter(source, source.InlineData)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1

	first, err := reader.Read()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		if importBatchSensitiveBindingName(source.Name) || importBatchContainsSensitiveColumn(source.Columns) {
			return unmanagedImportBatchCredential(path)
		}
		return nil
	}
	columns := append([]string(nil), source.Columns...)
	firstIsData := !source.FirstRowHeader
	if source.FirstRowHeader {
		columns = first
	}
	sensitive := make(map[int]struct{})
	for index, column := range columns {
		if importBatchSensitiveBindingName(column) {
			sensitive[index] = struct{}{}
		}
	}
	if importBatchSensitiveBindingName(source.Name) && len(sensitive) == 0 {
		width := len(columns)
		if width == 0 {
			width = len(first)
		}
		for index := 0; index < width; index++ {
			sensitive[index] = struct{}{}
		}
	}
	if len(sensitive) == 0 {
		// Ordinary columns are intentionally not inspected or constrained.
		return nil
	}
	validateRecord := func(record []string, recordIndex int) error {
		for columnIndex := range sensitive {
			if columnIndex >= len(record) {
				continue
			}
			if err := validateImportBatchCredentialScalar(
				record[columnIndex],
				false,
				fmt.Sprintf("%s.records[%d].value", path, recordIndex),
				context,
			); err != nil {
				return err
			}
		}
		return nil
	}
	recordIndex := 0
	if firstIsData {
		if err := validateRecord(first, recordIndex); err != nil {
			return err
		}
		recordIndex++
	}
	for recordIndex <= limits.MaxValues {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return unmanagedImportBatchCredential(path)
		}
		if err := validateRecord(record, recordIndex); err != nil {
			return err
		}
		recordIndex++
	}
	return unmanagedImportBatchCredential(path)
}

func validateImportBatchFileDataSourceCredentials(
	source dsl.DataSource,
	path string,
	context importBatchCredentialContext,
) error {
	limits := credentialpolicy.DefaultLimits()
	sensitiveIdentity := importBatchSensitiveBindingName(source.Name) ||
		importBatchSensitiveBindingName(source.FileName)
	payload := []byte(source.InlineData)
	encoding := strings.ToLower(strings.TrimSpace(source.Encoding))
	switch encoding {
	case "", "text":
		if len(payload) > limits.MaxBytes {
			return unmanagedImportBatchCredential(path)
		}
	case "base64":
		// Match the runtime's whitespace-tolerant decoder, while bounding both
		// source work and the decoded allocation used for credential inspection.
		// Anything outside that audit bound fails closed before receipt storage.
		maxEncodedBytes := base64.StdEncoding.EncodedLen(limits.MaxBytes)
		if len(source.InlineData) > maxEncodedBytes+1024 {
			return unmanagedImportBatchCredential(path)
		}
		compact := strings.Join(strings.Fields(source.InlineData), "")
		if base64.StdEncoding.DecodedLen(len(compact)) > limits.MaxBytes {
			return unmanagedImportBatchCredential(path)
		}
		decoded, err := base64.StdEncoding.DecodeString(compact)
		if err != nil {
			return unmanagedImportBatchCredential(path)
		}
		defer clear(decoded)
		if len(decoded) > limits.MaxBytes {
			return unmanagedImportBatchCredential(path)
		}
		payload = decoded
	default:
		// The runtime rejects unknown file encodings before transport, but the
		// immutable receipt would still retain bytes that this boundary could
		// not interpret. Fail closed instead of persisting an unreviewed draft.
		return unmanagedImportBatchCredential(path)
	}

	body := string(payload)
	if sensitiveIdentity {
		if err := validateImportBatchCredentialScalar(
			body,
			false,
			path+".inline_data",
			context,
		); err != nil {
			return err
		}
	}
	return validateImportBatchStructuredBody(
		body,
		source.ContentType,
		path+".inline_data",
		context,
	)
}

func importBatchContainsSensitiveColumn(columns []string) bool {
	for _, column := range columns {
		if importBatchSensitiveBindingName(column) {
			return true
		}
	}
	return false
}

func importBatchSensitiveBindingName(name string) bool {
	// Parameters and data-source columns are structured field names, not HTTP
	// headers. Use the component-aware credential vocabulary so ordinary
	// columns such as "key" are not rejected by the intentionally broader
	// header heuristic.
	return credential.SensitiveName(name)
}

func validateImportBatchConfigCredentials(script *dsl.Script, context importBatchCredentialContext) error {
	if err := validateImportBatchURL(script.Config.BaseURL, "config.base_url", context); err != nil {
		return err
	}
	if err := validateImportBatchHeaders(
		script.Config.HTTP.DefaultHeaders,
		script.Config.HTTP.DefaultHeaderList,
		"config.http.default_headers",
		context,
	); err != nil {
		return err
	}
	if err := validateImportBatchAuth(&script.Config.HTTP.Auth, "config.http.auth", context); err != nil {
		return err
	}
	if err := validateImportBatchURL(script.Config.HTTP.Proxy.URL, "config.http.proxy.url", context); err != nil {
		return err
	}
	if err := validateImportBatchCredentialScalar(script.Config.HTTP.Proxy.Password, false, "config.http.proxy.password", context); err != nil {
		return err
	}
	for _, binding := range script.Config.Bruno.SecretBindings {
		if strings.TrimSpace(binding) == "" {
			continue
		}
		if _, ok := credential.ParseReference(binding); !ok {
			return unmanagedImportBatchCredential("config.bruno.secret_bindings")
		}
	}
	if script.Config.SOAP.WSSecurity != nil && script.Config.SOAP.WSSecurity.UsernameToken != nil {
		if err := validateImportBatchCredentialScalar(
			script.Config.SOAP.WSSecurity.UsernameToken.Password,
			false,
			"config.soap.ws_security.username_token.password",
			context,
		); err != nil {
			return err
		}
	}
	for _, namespaceURI := range script.Config.SOAP.Namespaces {
		if err := validateImportBatchURL(
			namespaceURI,
			"config.soap.namespaces.value",
			context,
		); err != nil {
			return err
		}
		if err := validateImportBatchOpaqueCredentialString(
			namespaceURI,
			"config.soap.namespaces.value",
			context,
			false,
		); err != nil {
			return err
		}
	}
	credentialValues := []struct {
		value string
		path  string
	}{
		{script.Config.Database.DSN, "config.database.dsn"},
		{script.Config.Network.Password, "config.network.password"},
		{script.Config.SAPGUI.ConnectionString, "config.sap_gui.connection_string"},
		{script.Config.SAPGUI.Password, "config.sap_gui.password"},
		{script.Config.JMS.Password, "config.jms.password"},
		{script.Config.Kafka.SASLPassword, "config.kafka.sasl_password"},
		{script.Config.EventHubs.ConnectionString, "config.eventhubs.connection_string"},
		{script.Config.Browser.HTTPCredentials.Password, "config.browser.http_credentials.password"},
		{script.Config.Browser.Proxy.Password, "config.browser.proxy.password"},
	}
	for _, item := range credentialValues {
		if err := validateImportBatchCredentialScalar(item.value, false, item.path, context); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		value string
		path  string
	}{
		{script.Config.Database.DSNSecret, "config.database.dsn_secret"},
		{script.Config.Kafka.SASLPasswordSecret, "config.kafka.sasl_password_secret"},
		{script.Config.EventHubs.ConnectionStringSecret, "config.eventhubs.connection_string_secret"},
	} {
		if err := validateImportBatchDedicatedSecretReference(item.value, item.path); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		value string
		path  string
	}{
		{script.Config.Network.Address, "config.network.address"},
		{script.Config.WebSocket.Origin, "config.websocket.origin"},
		{script.Config.JMS.BrokerURL, "config.jms.broker_url"},
		{script.Config.Kafka.SchemaRegistryURL, "config.kafka.schema_registry_url"},
		{script.Config.Browser.HTTPCredentials.Origin, "config.browser.http_credentials.origin"},
	} {
		if err := validateImportBatchURL(item.value, item.path, context); err != nil {
			return err
		}
	}
	if err := validateImportBatchOpaqueCredentialString(
		script.Config.JMS.BrokerURL,
		"config.jms.broker_url",
		context,
		false,
	); err != nil {
		return err
	}
	for index, argument := range script.Config.JavaVuser.AdditionalVMParameters {
		if err := validateImportBatchOpaqueCredentialString(
			argument,
			fmt.Sprintf("config.java_vuser.additional_vm_parameters[%d]", index),
			context,
			false,
		); err != nil {
			return err
		}
	}
	if err := validateImportBatchOpaqueCredentialString(
		script.Config.JavaVuser.RunnerCommand,
		"config.java_vuser.runner_command",
		context,
		false,
	); err != nil {
		return err
	}
	if err := validateImportBatchExecutableMap(
		script.Config.WebRuntime.AdditionalAttributes,
		"config.web_runtime.additional_attributes",
		context,
	); err != nil {
		return err
	}
	for index := range script.Config.WebRuntime.DataFormatting.Chain {
		if err := validateImportBatchExecutableMap(
			script.Config.WebRuntime.DataFormatting.Chain[index].Options,
			fmt.Sprintf("config.web_runtime.data_formatting.chain[%d].options", index),
			context,
		); err != nil {
			return err
		}
	}
	for index := range script.Config.Browser.ClientCertificates {
		if err := validateImportBatchURL(
			script.Config.Browser.ClientCertificates[index].Origin,
			fmt.Sprintf("config.browser.client_certificates[%d].origin", index),
			context,
		); err != nil {
			return err
		}
		if err := validateImportBatchCredentialScalar(
			script.Config.Browser.ClientCertificates[index].Passphrase,
			false,
			fmt.Sprintf("config.browser.client_certificates[%d].passphrase", index),
			context,
		); err != nil {
			return err
		}
	}
	if err := validateImportBatchURL(script.Config.Browser.Proxy.Server, "config.browser.proxy.server", context); err != nil {
		return err
	}
	if err := validateImportBatchHeaders(script.Config.GRPC.Metadata, nil, "config.grpc.metadata", context); err != nil {
		return err
	}
	if err := validateImportBatchExecutableMap(script.Config.JMS.Properties, "config.jms.properties", context); err != nil {
		return err
	}
	if err := validateImportBatchExecutableMap(script.Config.Kafka.Properties, "config.kafka.properties", context); err != nil {
		return err
	}
	for index := range script.Config.Observability.ExternalMetrics {
		if err := validateImportBatchURL(
			script.Config.Observability.ExternalMetrics[index].URL,
			fmt.Sprintf("config.observability.external_metrics[%d].url", index),
			context,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateImportBatchDedicatedSecretReference(value, path string) error {
	value = strings.TrimSpace(value)
	if value == "" || credential.ValidLogicalName(value) {
		return nil
	}
	if _, ok := credential.ParseReference(value); ok {
		return nil
	}
	return unmanagedImportBatchCredential(path)
}

func validateImportBatchActionCredentials(
	action *dsl.Action,
	path string,
	context importBatchCredentialContext,
) error {
	if action == nil {
		return nil
	}
	if err := validateImportBatchURL(action.Target, path+".target", context); err != nil {
		return err
	}
	if err := validateImportBatchHeaders(action.Headers, action.HeaderList, path+".headers", context); err != nil {
		return err
	}
	if err := validateImportBatchAuth(action.Auth, path+".auth", context); err != nil {
		return err
	}
	contentType := importBatchContentType(action.Headers, action.HeaderList)
	wireBody := action.Body
	if decoded, encoded, err := dsl.DecodeBodyEncoding(
		action.Body,
		action.BodyEncoding,
		credentialpolicy.DefaultLimits().MaxBytes,
	); encoded {
		if err != nil {
			return unmanagedImportBatchCredential(path + ".body")
		}
		wireBody = string(decoded)
	}
	if err := validateImportBatchStructuredBody(wireBody, contentType, path+".body", context); err != nil {
		return err
	}
	if action.SOAP != nil {
		if err := validateImportBatchURL(
			action.SOAP.Action,
			path+".soap.action",
			context,
		); err != nil {
			return err
		}
		if err := validateImportBatchOpaqueCredentialString(
			action.SOAP.Action,
			path+".soap.action",
			context,
			false,
		); err != nil {
			return err
		}
		if err := validateImportBatchOpaqueCredentialString(
			action.SOAP.Operation,
			path+".soap.operation",
			context,
			false,
		); err != nil {
			return err
		}
	}
	if strings.TrimSpace(action.Value) != "" &&
		(credential.SensitiveName(action.Name) ||
			credential.SensitiveName(action.Type) ||
			credential.SensitiveName(action.Target) ||
			credential.SensitiveName(action.Selector) ||
			credential.SensitiveName(action.ObjectRef)) {
		if err := validateImportBatchCredentialScalar(action.Value, false, path+".value", context); err != nil {
			return err
		}
	}
	if err := validateImportBatchMetadataCredentials(
		action.Metadata,
		path+".metadata",
		context,
		false,
	); err != nil {
		return err
	}
	return validateImportBatchExecutableMap(action.Params, path+".params", context)
}

func importBatchJSONValue(value any) (any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// validateImportBatchMetadataCredentials covers metadata that can otherwise
// bypass the typed request surfaces. In particular, JMS publishes string
// action metadata as outbound message properties. Review/gate metadata is also
// copied into immutable receipt snapshots, so literal credential values must
// be rejected before either write occurs.
//
// Metadata uses the component-aware sensitive-name vocabulary rather than the
// broader HTTP-header heuristic: ordinary provenance fields named "key" are
// descriptors, not credentials. Sibling name/value shapes still apply the
// header vocabulary to the semantic name.
func validateImportBatchMetadataCredentials(
	value any,
	path string,
	context importBatchCredentialContext,
	inheritedSensitive bool,
) error {
	switch typed := value.(type) {
	case map[string]any:
		semanticSensitive := importBatchMetadataHasSensitiveSemanticName(typed)
		for name, child := range typed {
			childPath := path + ".value"
			sensitive := inheritedSensitive ||
				credential.SensitiveName(name) ||
				(semanticSensitive && importBatchMetadataPayloadKey(name))
			if sensitive {
				if text, ok := child.(string); ok {
					if importBatchDisclosureSafeMetadataValue(text) ||
						importBatchMetadataDescriptor(name, text) {
						continue
					}
					if err := validateImportBatchCredentialScalar(
						text,
						strings.Contains(strings.ToLower(name), "authorization"),
						childPath,
						context,
					); err != nil {
						return err
					}
					continue
				}
				// Boolean/number flags do not carry plaintext credential bytes.
				// Containers still need recursive inspection for nested values.
				switch child.(type) {
				case map[string]any, map[string]string, []any, []map[string]any:
					childSensitive := importBatchMetadataCredentialContainer(name) ||
						(semanticSensitive && importBatchMetadataPayloadKey(name))
					if err := validateImportBatchMetadataCredentials(
						child,
						childPath,
						context,
						childSensitive,
					); err != nil {
						return err
					}
				}
				continue
			}
			if text, ok := child.(string); ok {
				if err := validateImportBatchOpaqueCredentialString(
					text,
					childPath,
					context,
					true,
				); err != nil {
					return err
				}
				continue
			}
			if err := validateImportBatchMetadataCredentials(
				child,
				childPath,
				context,
				false,
			); err != nil {
				return err
			}
		}
	case map[string]string:
		generic := make(map[string]any, len(typed))
		for name, child := range typed {
			generic[name] = child
		}
		return validateImportBatchMetadataCredentials(generic, path, context, inheritedSensitive)
	case []any:
		for index := range typed {
			if err := validateImportBatchMetadataCredentials(
				typed[index],
				fmt.Sprintf("%s[%d]", path, index),
				context,
				inheritedSensitive,
			); err != nil {
				return err
			}
		}
	case []map[string]any:
		for index := range typed {
			if err := validateImportBatchMetadataCredentials(
				typed[index],
				fmt.Sprintf("%s[%d]", path, index),
				context,
				inheritedSensitive,
			); err != nil {
				return err
			}
		}
	case string:
		if !inheritedSensitive ||
			importBatchDisclosureSafeMetadataValue(typed) ||
			importBatchMetadataDescriptor("", typed) {
			return nil
		}
		return validateImportBatchCredentialScalar(typed, false, path, context)
	}
	return nil
}

func importBatchMetadataHasSensitiveSemanticName(value map[string]any) bool {
	for key, child := range value {
		switch credential.NormalizeName(key) {
		case "key", "name", "field", "fieldname", "header", "headername", "property":
		default:
			continue
		}
		text, ok := child.(string)
		if ok && (credential.SensitiveName(text) || credential.SensitiveHeader(text)) {
			return true
		}
	}
	return false
}

func importBatchMetadataPayloadKey(name string) bool {
	switch credential.NormalizeName(name) {
	case "value", "text", "content", "raw", "source", "arguments":
		return true
	default:
		return false
	}
}

func importBatchMetadataCredentialContainer(name string) bool {
	switch credential.NormalizeName(name) {
	case "auth", "authentication", "authorization", "credential", "credentials",
		"password", "secret", "secrets", "token":
		return true
	default:
		return false
	}
}

func importBatchDisclosureSafeMetadataValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == credential.Redacted {
		return true
	}
	for _, scheme := range []string{"bearer", "basic", "digest"} {
		if len(trimmed) <= len(scheme) ||
			!strings.EqualFold(trimmed[:len(scheme)], scheme) ||
			!isImportBatchSpace(trimmed[len(scheme)]) {
			continue
		}
		return strings.TrimSpace(trimmed[len(scheme):]) == credential.Redacted
	}
	return false
}

func importBatchMetadataDescriptor(name, value string) bool {
	normalized := credential.NormalizeName(name)
	if normalized == "" {
		return false
	}
	if normalized == "effectiveauth" {
		return importBatchCredentialDescriptor("auth_mode", value)
	}
	if importBatchCredentialDescriptor(name, value) {
		return true
	}
	descriptor := false
	for _, suffix := range []string{
		"ref", "name", "id", "function", "hash",
	} {
		descriptor = descriptor || strings.HasSuffix(normalized, suffix)
	}
	if !descriptor {
		return false
	}
	value = strings.TrimSpace(value)
	return value != "" &&
		len(value) <= credential.MaxLogicalNameBytes &&
		importBatchRuntimeBindingName(value)
}

func validateImportBatchHeaders(
	headers map[string]string,
	headerList dsl.HeaderList,
	path string,
	context importBatchCredentialContext,
) error {
	for index := range headerList {
		if !credential.SensitiveHeader(headerList[index].Name) {
			continue
		}
		if err := validateImportBatchHeaderScalar(
			headerList[index].Name,
			headerList[index].Value,
			fmt.Sprintf("%s.header_list[%d]", path, index),
			context,
		); err != nil {
			return err
		}
	}
	for name, value := range headers {
		if !credential.SensitiveHeader(name) {
			continue
		}
		if err := validateImportBatchHeaderScalar(name, value, path+".map", context); err != nil {
			return err
		}
	}
	return nil
}

func validateImportBatchHeaderScalar(
	name,
	value,
	path string,
	context importBatchCredentialContext,
) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if importBatchGovernedCredentialValue(value, true, context) {
		return nil
	}
	if credential.CookieHeader(name) && importBatchGovernedCookieHeader(value, context) {
		return nil
	}
	return unmanagedImportBatchCredential(path)
}

func importBatchGovernedCookieHeader(value string, context importBatchCredentialContext) bool {
	parts := strings.Split(value, ";")
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		name, candidate, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found || strings.TrimSpace(name) == "" {
			return false
		}
		candidate = strings.TrimSpace(candidate)
		if len(candidate) >= 2 &&
			((candidate[0] == '"' && candidate[len(candidate)-1] == '"') ||
				(candidate[0] == '\'' && candidate[len(candidate)-1] == '\'')) {
			candidate = strings.TrimSpace(candidate[1 : len(candidate)-1])
		}
		if !importBatchWholeRuntimeTemplate(candidate, context) {
			return false
		}
	}
	return true
}

func validateImportBatchAuth(
	auth *dsl.AuthConfig,
	path string,
	context importBatchCredentialContext,
) error {
	if auth == nil {
		return nil
	}
	if err := validateImportBatchCredentialScalar(auth.Password, false, path+".password", context); err != nil {
		return err
	}
	return validateImportBatchCredentialScalar(auth.Token, false, path+".token", context)
}

func validateImportBatchCredentialScalar(
	value string,
	allowAuthScheme bool,
	path string,
	context importBatchCredentialContext,
) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if importBatchGovernedCredentialValue(value, allowAuthScheme, context) {
		return nil
	}
	return unmanagedImportBatchCredential(path)
}

func importBatchGovernedCredentialValue(
	value string,
	allowAuthScheme bool,
	context importBatchCredentialContext,
) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.Contains(trimmed, credential.Redacted) {
		return false
	}
	if importBatchWholeRuntimeTemplate(trimmed, context) {
		return true
	}
	if !allowAuthScheme {
		return false
	}
	for _, scheme := range []string{"bearer", "basic", "digest"} {
		if len(trimmed) <= len(scheme) ||
			!strings.EqualFold(trimmed[:len(scheme)], scheme) ||
			!isImportBatchSpace(trimmed[len(scheme)]) {
			continue
		}
		return importBatchWholeRuntimeTemplate(strings.TrimSpace(trimmed[len(scheme):]), context)
	}
	return false
}

func importBatchWholeRuntimeTemplate(value string, context importBatchCredentialContext) bool {
	value = strings.TrimSpace(value)
	if len(value) < 5 || len(value) > 260 ||
		!strings.HasPrefix(value, "{{") ||
		!strings.HasSuffix(value, "}}") ||
		strings.ContainsAny(value, "\r\n") {
		return false
	}
	inner := value[2 : len(value)-2]
	if strings.Contains(inner, "{{") || strings.Contains(inner, "}}") {
		return false
	}
	expression := strings.TrimSpace(inner)
	namespace, name, found := strings.Cut(expression, ".")
	if !found || strings.TrimSpace(name) == "" {
		return false
	}
	switch strings.TrimSpace(namespace) {
	case "secret":
		_, ok := credential.ParseReference(value)
		return ok
	case "env":
		return importBatchRuntimeBindingName(name)
	case "var":
		return importBatchRuntimeBindingName(name) && context.provesDynamicVariable(strings.TrimSpace(name))
	default:
		return false
	}
}

func importBatchDynamicVariableName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > credential.MaxLogicalNameBytes {
		return false
	}
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case strings.ContainsRune("._-", char):
		default:
			return false
		}
	}
	return true
}

func importBatchRuntimeBindingName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > credential.MaxLogicalNameBytes {
		return false
	}
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case strings.ContainsRune("._:/@-", char):
		default:
			return false
		}
	}
	return true
}

func isImportBatchSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func validateImportBatchURL(
	value,
	path string,
	context importBatchCredentialContext,
) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	projected, result, err := credentialpolicy.ProjectRawURL(value, credentialpolicy.Options{
		Mode:               credentialpolicy.Audit,
		Source:             credentialpolicy.SourceRef{Format: "import_batch", Path: path},
		TrustedWholeValues: importBatchTrustedTemplates(value, context),
	})
	if err != nil || importBatchProjectionUnsafe(result) || strings.Contains(projected, credential.Redacted) {
		return unmanagedImportBatchCredential(path)
	}
	return nil
}

func validateImportBatchStructuredBody(
	body,
	contentType,
	path string,
	context importBatchCredentialContext,
) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	limits := credentialpolicy.DefaultLimits()
	if len(body) > limits.MaxBytes {
		return unmanagedImportBatchCredential(path)
	}
	lowerContentType := strings.ToLower(strings.TrimSpace(contentType))
	trimmed := strings.TrimSpace(body)
	options := credentialpolicy.Options{
		Mode:               credentialpolicy.Audit,
		Source:             credentialpolicy.SourceRef{Format: "import_batch", Path: path},
		TrustedWholeValues: importBatchTrustedTemplates(body, context),
	}

	actualMultipartType, looksMultipart := importBatchMultipartMediaType(body, contentType)
	looksJSON := strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
	looksXML := strings.HasPrefix(trimmed, "<")
	looksForm := importBatchLooksFormEncoded(trimmed)
	looksGraphQL := importBatchLooksGraphQL(trimmed)
	hasShape := looksMultipart || looksJSON || looksXML || looksForm || looksGraphQL

	// Source bytes are authoritative. A missing or misleading Content-Type must
	// not hide a credential-bearing structured body from the persistence
	// boundary, so sniff every recognizable wire shape first.
	if looksMultipart {
		if err := validateImportBatchMultipart(body, actualMultipartType, path, context); err != nil {
			return err
		}
	}
	if looksJSON {
		_, result, err := credentialpolicy.ProjectJSON([]byte(body), options)
		if err != nil || importBatchProjectionUnsafe(result) {
			return unmanagedImportBatchCredential(path)
		}
		if query := importBatchGraphQLQuery(body); query != "" {
			_, graphqlResult, graphqlErr := credentialpolicy.ProjectGraphQL(
				credentialpolicy.GraphQL{Query: []byte(query)},
				options,
			)
			if graphqlErr != nil || importBatchProjectionUnsafe(graphqlResult) {
				return unmanagedImportBatchCredential(path)
			}
		}
	}
	if looksXML {
		_, result, err := credentialpolicy.ProjectXML([]byte(body), options)
		if err != nil || importBatchProjectionUnsafe(result) {
			return unmanagedImportBatchCredential(path)
		}
	}
	if looksForm {
		_, _, result, err := credentialpolicy.ProjectFormURLEncoded(body, options)
		if err != nil || importBatchProjectionUnsafe(result) {
			return unmanagedImportBatchCredential(path)
		}
	}
	if looksGraphQL {
		_, result, err := credentialpolicy.ProjectGraphQL(
			credentialpolicy.GraphQL{Query: []byte(body)},
			options,
		)
		if err != nil || importBatchProjectionUnsafe(result) {
			return unmanagedImportBatchCredential(path)
		}
	}

	// For unrecognized bytes, retain declared-type validation as a secondary
	// signal. This preserves existing behavior for syntactically unusual but
	// correctly labelled bodies without allowing the label to override a
	// different recognizable wire shape.
	if !hasShape {
		var result credentialpolicy.Result
		var err error
		switch {
		case strings.Contains(lowerContentType, "multipart/form-data"):
			if err := validateImportBatchMultipart(body, contentType, path, context); err != nil {
				return err
			}
		case strings.Contains(lowerContentType, "application/graphql"):
			_, result, err = credentialpolicy.ProjectGraphQL(
				credentialpolicy.GraphQL{Query: []byte(body)},
				options,
			)
		case strings.Contains(lowerContentType, "json"):
			_, result, err = credentialpolicy.ProjectJSON([]byte(body), options)
		case strings.Contains(lowerContentType, "xml"):
			_, result, err = credentialpolicy.ProjectXML([]byte(body), options)
		case strings.Contains(lowerContentType, "x-www-form-urlencoded"):
			_, _, result, err = credentialpolicy.ProjectFormURLEncoded(body, options)
		}
		if err != nil || importBatchProjectionUnsafe(result) {
			return unmanagedImportBatchCredential(path)
		}
	}
	if !hasShape && scrub.RedactCredentialBody(body) != body {
		return unmanagedImportBatchCredential(path)
	}
	return nil
}

func importBatchMultipartMediaType(body, contentType string) (string, bool) {
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "content-disposition:") ||
		!strings.Contains(lower, "form-data") {
		return "", false
	}
	if mediaType, parameters, err := mime.ParseMediaType(contentType); err == nil &&
		strings.EqualFold(mediaType, "multipart/form-data") &&
		strings.TrimSpace(parameters["boundary"]) != "" {
		return contentType, true
	}
	line := body
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if !strings.HasPrefix(line, "--") {
		return contentType, true
	}
	boundary := strings.TrimSpace(strings.TrimPrefix(line, "--"))
	if strings.HasSuffix(boundary, "--") {
		boundary = strings.TrimSuffix(boundary, "--")
	}
	if boundary == "" {
		return contentType, true
	}
	return mime.FormatMediaType("multipart/form-data", map[string]string{"boundary": boundary}), true
}

func validateImportBatchMultipart(
	body,
	contentType,
	path string,
	context importBatchCredentialContext,
) error {
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || strings.TrimSpace(parameters["boundary"]) == "" {
		if scrub.RedactCredentialBody(body) != body {
			return unmanagedImportBatchCredential(path)
		}
		return nil
	}
	boundary := parameters["boundary"]
	reader := multipart.NewReader(strings.NewReader(body), boundary)
	limits := credentialpolicy.DefaultLimits()
	input := credentialpolicy.Multipart{Boundary: boundary}
	trusted := importBatchTrustedTemplates(body, context)
	for len(input.Parts) <= limits.MaxParts {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			if scrub.RedactCredentialBody(body) != body {
				return unmanagedImportBatchCredential(path)
			}
			return nil
		}
		payload, readErr := io.ReadAll(io.LimitReader(part, int64(limits.MaxBytes)+1))
		if readErr != nil {
			return unmanagedImportBatchCredential(path)
		}
		headers := make([]credentialpolicy.HeaderOccurrence, 0, len(part.Header))
		for name, values := range part.Header {
			for _, value := range values {
				headers = append(headers, credentialpolicy.HeaderOccurrence{Name: name, Value: value})
				if importBatchGovernedCredentialValue(value, true, context) ||
					(credential.CookieHeader(name) && importBatchGovernedCookieHeader(value, context)) {
					trusted[strings.TrimSpace(value)] = struct{}{}
				}
			}
		}
		input.Parts = append(input.Parts, credentialpolicy.MultipartPart{
			Headers:     headers,
			Name:        part.FormName(),
			FileName:    part.FileName(),
			ContentType: part.Header.Get("Content-Type"),
			Body:        payload,
		})
	}
	options := credentialpolicy.Options{
		Mode:               credentialpolicy.Audit,
		Source:             credentialpolicy.SourceRef{Format: "import_batch", Path: path},
		TrustedWholeValues: trusted,
	}
	_, result, err := credentialpolicy.ProjectMultipart(input, options)
	if err != nil || importBatchProjectionUnsafe(result) {
		return unmanagedImportBatchCredential(path)
	}
	return nil
}

func importBatchProjectionUnsafe(result credentialpolicy.Result) bool {
	if result.Disposition == credentialpolicy.Rejected {
		return true
	}
	for _, finding := range result.Findings {
		switch finding.Code {
		case "import_credentials_unmanaged",
			"credential_xml_sensitive_mixed_content_review_required",
			"credential_graphql_inline_literal_unsupported":
			return true
		}
	}
	return false
}

func importBatchTrustedTemplates(
	value string,
	context importBatchCredentialContext,
) map[string]struct{} {
	trusted := make(map[string]struct{})
	for _, candidate := range importBatchRuntimeTemplatePattern.FindAllString(value, -1) {
		if importBatchWholeRuntimeTemplate(candidate, context) {
			trusted[strings.TrimSpace(candidate)] = struct{}{}
		}
	}
	return trusted
}

func importBatchGraphQLQuery(body string) string {
	var envelope struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Query)
}

func importBatchLooksFormEncoded(value string) bool {
	if strings.ContainsAny(value, "\r\n<>") || !strings.Contains(value, "=") {
		return false
	}
	first, _, _ := strings.Cut(value, "&")
	name, _, found := strings.Cut(first, "=")
	return found && strings.TrimSpace(name) != ""
}

func importBatchLooksGraphQL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"query", "mutation", "subscription", "fragment"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		if len(lower) == len(prefix) {
			return true
		}
		switch lower[len(prefix)] {
		case ' ', '\t', '\r', '\n', '(', '{':
			return true
		}
	}
	return false
}

func importBatchContentType(headers map[string]string, headerList dsl.HeaderList) string {
	var result string
	for _, header := range headerList {
		if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Type") {
			result = header.Value
		}
	}
	if result != "" {
		return result
	}
	for name, value := range headers {
		if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			return value
		}
	}
	return ""
}

func validateImportBatchExecutableMap(
	value any,
	path string,
	context importBatchCredentialContext,
) error {
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			childPath := path + ".value"
			if credential.SensitiveName(name) || credential.SensitiveHeader(name) {
				text, ok := child.(string)
				if !ok {
					if importBatchCredentialContainer(name) {
						if err := validateImportBatchExecutableMap(child, childPath, context); err != nil {
							return err
						}
						continue
					}
					return unmanagedImportBatchCredential(childPath)
				}
				if importBatchCredentialDescriptor(name, text) {
					continue
				}
				if err := validateImportBatchCredentialScalar(
					text,
					strings.Contains(strings.ToLower(name), "authorization"),
					childPath,
					context,
				); err != nil {
					return err
				}
				continue
			}
			if text, ok := child.(string); ok {
				if err := validateImportBatchOpaqueCredentialString(
					text,
					childPath,
					context,
					false,
				); err != nil {
					return err
				}
				continue
			}
			if err := validateImportBatchExecutableMap(child, childPath, context); err != nil {
				return err
			}
		}
	case map[string]string:
		for name, child := range typed {
			if credential.SensitiveName(name) || credential.SensitiveHeader(name) {
				if importBatchCredentialDescriptor(name, child) {
					continue
				}
				if err := validateImportBatchCredentialScalar(
					child,
					strings.Contains(strings.ToLower(name), "authorization"),
					path+".value",
					context,
				); err != nil {
					return err
				}
				continue
			}
			if err := validateImportBatchOpaqueCredentialString(
				child,
				path+".value",
				context,
				false,
			); err != nil {
				return err
			}
		}
	case []any:
		for index := range typed {
			if err := validateImportBatchExecutableMap(
				typed[index],
				fmt.Sprintf("%s[%d]", path, index),
				context,
			); err != nil {
				return err
			}
		}
	case []map[string]any:
		for index := range typed {
			if err := validateImportBatchExecutableMap(
				typed[index],
				fmt.Sprintf("%s[%d]", path, index),
				context,
			); err != nil {
				return err
			}
		}
	case string:
		return validateImportBatchOpaqueCredentialString(
			typed,
			path,
			context,
			false,
		)
	}
	return nil
}

// validateImportBatchOpaqueCredentialString catches credentials embedded in a
// neutral map property, such as metadata.config="password=literal" or
// params.options="Bearer literal". Kafka and JMS put string metadata on the
// wire, while multiple protocol adapters interpret opaque params themselves,
// so key-only classification is not sufficient at this persistence boundary.
func validateImportBatchOpaqueCredentialString(
	value,
	path string,
	context importBatchCredentialContext,
	allowRedacted bool,
) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if err := validateImportBatchDelimitedCredentialAssignments(
		value,
		path,
		context,
	); err != nil {
		return err
	}
	if importBatchAuthenticationSchemeValue(trimmed) {
		if allowRedacted && importBatchDisclosureSafeMetadataValue(trimmed) {
			return nil
		}
		return validateImportBatchCredentialScalar(trimmed, true, path, context)
	}
	if strings.Contains(value, credential.Redacted) {
		if !allowRedacted {
			return unmanagedImportBatchCredential(path)
		}
		// Validate the rest of a disclosure-safe draft without treating its
		// sentinel as source credential material. A second literal credential
		// in the same string remains visible to the structured audit.
		value = strings.ReplaceAll(value, credential.Redacted, "{{secret.APEX_REDACTED}}")
	}
	if scrub.RedactCredentialBody(value) == value {
		return nil
	}
	return validateImportBatchStructuredBody(value, "", path, context)
}

func validateImportBatchDelimitedCredentialAssignments(
	value,
	path string,
	context importBatchCredentialContext,
) error {
	limits := credentialpolicy.DefaultLimits()
	if len(value) > limits.MaxBytes {
		return unmanagedImportBatchCredential(path)
	}
	segments := strings.FieldsFunc(value, func(char rune) bool {
		switch char {
		case ';', '&', '\r', '\n':
			return true
		default:
			return false
		}
	})
	if len(segments) > limits.MaxValues {
		return unmanagedImportBatchCredential(path)
	}
	for _, segment := range segments {
		name, candidate, found := strings.Cut(segment, "=")
		if !found || !credential.SensitiveName(strings.TrimSpace(name)) {
			continue
		}
		if err := validateImportBatchCredentialScalar(
			strings.TrimSpace(candidate),
			strings.Contains(strings.ToLower(name), "authorization"),
			path,
			context,
		); err != nil {
			return err
		}
	}
	return nil
}

func importBatchAuthenticationSchemeValue(value string) bool {
	value = strings.TrimSpace(value)
	for _, scheme := range []string{"bearer", "basic", "digest"} {
		if len(value) > len(scheme) &&
			strings.EqualFold(value[:len(scheme)], scheme) &&
			isImportBatchSpace(value[len(scheme)]) {
			return true
		}
	}
	return false
}

func importBatchCredentialContainer(name string) bool {
	switch credential.NormalizeName(name) {
	case "auth", "authentication", "credential", "credentials":
		return true
	default:
		return false
	}
}

func importBatchCredentialDescriptor(name, value string) bool {
	normalizedName := credential.NormalizeName(name)
	descriptor := strings.HasSuffix(normalizedName, "type") ||
		strings.HasSuffix(normalizedName, "scheme") ||
		strings.HasSuffix(normalizedName, "mode")
	if !descriptor && !importBatchCredentialContainer(name) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "inherit", "basic", "bearer", "digest", "ntlm", "kerberos", "negotiate":
		return true
	default:
		return false
	}
}

func unmanagedImportBatchCredential(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return ErrImportBatchCredentialsUnmanaged
	}
	return fmt.Errorf("%w at %s", ErrImportBatchCredentialsUnmanaged, path)
}
