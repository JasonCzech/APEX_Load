package api

import (
	"maps"
	"net/http"
	"sort"
)

// The shared response builder hides negotiation from the bounded handler AST
// walker. List routes that publish that envelope, not internal uses that retain
// only its credential capabilities (for example JMeter repair application).
func applyCanonicalImportContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil || route.Method != http.MethodPost {
		return
	}
	component := negotiatedImportResponseComponent(route.Path)
	if component == "" {
		return
	}
	if component != "EnterpriseArchiveReviewResponse" {
		for status, response := range contract.Responses {
			if len(status) != 3 || status[0] != '2' {
				continue
			}
			media := response.Content["application/json"]
			if len(media.Schema.Properties) == 0 && media.Schema.Ref == "" && len(media.Schema.OneOf) == 0 {
				media.Schema = oasSchema{Ref: "#/components/schemas/" + component}
			} else if component == "ScriptImportReviewResponse" && media.Schema.Ref == "" && len(media.Schema.OneOf) == 0 {
				// Preserve any route-specific inferred fields and requirements.
				applyCanonicalImportResponseSchema(&media.Schema)
			}
			if response.Content == nil {
				response.Content = map[string]oasMediaType{}
			}
			response.Content["application/json"] = media
			contract.Responses[status] = response
		}
	}
	if route.Path == "/api/v1/scripts/import/recorder/replay" {
		if contract.Responses == nil {
			contract.Responses = map[string]oasResponse{}
		}
		response := contract.Responses["400"]
		errorSchema := response.Content["application/json"].Schema
		if len(errorSchema.OneOf) == 2 && errorSchema.OneOf[1].Ref == "#/components/schemas/RecorderImportReplayInvalidResponse" {
			errorSchema = errorSchema.OneOf[0]
		}
		if errorSchema.Ref == "" && len(errorSchema.Properties) == 0 && len(errorSchema.OneOf) == 0 {
			errorSchema = oasSchema{Ref: "#/components/schemas/ErrorResponse"}
		}
		response.Description = "Invalid request, or converted recording refused by replay validation. Validation refusals retain the negotiated review plus valid=false and issues; decoding errors retain the ordinary error response."
		response.Content = map[string]oasMediaType{"application/json": {Schema: oasSchema{OneOf: []oasSchema{
			errorSchema, {Ref: "#/components/schemas/RecorderImportReplayInvalidResponse"},
		}}}}
		contract.Responses["400"] = response
	}
	parameter := oasParameter{
		Name: "response_format", In: "query", Schema: oasParamSchema{Type: "string"},
		Description: "Set exactly canonical-v1 for the controller's compact import review: drafts occur in scripts and primary_script_index selects the primary. Omitted or unrecognized values retain legacy inline aliases. Archive collection members carry their own negotiated response; the collection envelope is unchanged. Saved recording conversions retain their required saved-script alias in either format.",
	}
	found := false
	for i := range contract.QueryParameters {
		if contract.QueryParameters[i].Name == parameter.Name && contract.QueryParameters[i].In == parameter.In {
			contract.QueryParameters[i] = parameter
			found = true
			break
		}
	}
	if !found {
		contract.QueryParameters = append(contract.QueryParameters, parameter)
	}
	if route.Path == "/api/v1/scripts/import/har" || route.Path == "/api/v1/scripts/import/salesforce" {
		applyHARImportUploadContract(route, contract)
	}
	sort.Slice(contract.QueryParameters, func(i, j int) bool {
		return contract.QueryParameters[i].Name < contract.QueryParameters[j].Name
	})
}

func applyHARImportUploadContract(route discoveredRoute, contract *inferredRouteContract) {
	upsertQuery := func(param oasParameter) {
		for i := range contract.QueryParameters {
			if contract.QueryParameters[i].Name == param.Name && contract.QueryParameters[i].In == param.In {
				contract.QueryParameters[i] = param
				return
			}
		}
		contract.QueryParameters = append(contract.QueryParameters, param)
	}
	upsertQuery(oasParameter{
		Name: "project_id", In: "query", Schema: oasParamSchema{Type: "string"},
		Description: "Project scope for recording profiles, secret capability checks, and existing-script matching.",
	})
	upsertQuery(oasParameter{
		Name: "validate_only", In: "query", Schema: oasParamSchema{Type: "boolean"},
		Description: "When true, inspects and converts the capture without retaining source texts or allocating a source-secret session.",
	})
	upsertQuery(oasParameter{
		Name: "filename", In: "query", Schema: oasParamSchema{Type: "string"},
		Description: "Logical filename of the baseline HAR or ZIP upload when sent as application/json or application/octet-stream.",
	})

	allowComparison := route.Path == "/api/v1/scripts/import/har"
	if contract.RequestBody == nil {
		contract.RequestBody = &oasRequestBody{Required: true, Content: map[string]oasMediaType{}}
	}
	if contract.RequestBody.Content == nil {
		contract.RequestBody.Content = map[string]oasMediaType{}
	}
	if media, ok := contract.RequestBody.Content["multipart/form-data"]; ok {
		if media.Schema.Properties == nil {
			media.Schema.Properties = map[string]oasSchema{}
		}
		media.Schema.Properties["project_id"] = oasSchema{Type: "string", Description: "Project scope for recording profiles and secret capability checks."}
		media.Schema.Properties["validate_only"] = oasSchema{Type: "string", Description: "Boolean flag ('true'/'false') for stateless Stage 1/2 inspection without retaining source texts or allocating a source-secret session."}
		media.Schema.Properties["filter_client_telemetry"] = oasSchema{Type: "string", Description: "Boolean flag ('true'/'false') controlling default filtering of pure client-telemetry beacons."}
		if allowComparison {
			media.Schema.Properties["secondary_file"] = oasSchema{Type: "string", Format: "binary", Description: "Optional second HAR (.har or .zip) from an independent session used to classify dynamic vs static values."}
			media.Schema.Properties["comparison_file"] = oasSchema{Type: "string", Format: "binary", Description: "Alias of secondary_file."}
			media.Schema.Properties["file_b"] = oasSchema{Type: "string", Format: "binary", Description: "Alias of secondary_file."}
		}
		contract.RequestBody.Content["multipart/form-data"] = media
	}

	object := oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}
	jsonProps := map[string]oasSchema{
		"name":                        {Type: "string"},
		"project_id":                  {Type: "string"},
		"content":                     {Type: "string", Description: "Raw HAR JSON payload."},
		"validate_only":               {Type: "boolean", Description: "When true, runs stateless inspection without retaining source texts or allocating a source-secret session."},
		"recording_profile_id":        {Type: "string"},
		"profile_id":                  {Type: "string"},
		"conversion_policy":           {Type: "string"},
		"correlation_mode":            {Type: "string"},
		"resource_mode":               {Type: "string"},
		"http_version_mode":           {Type: "string"},
		"page_title_checks":           {Type: "boolean"},
		"include_assertions":          {Type: "boolean"},
		"infer_extractors":            {Type: "boolean"},
		"include_static_assets":       {Type: "boolean"},
		"redact_sensitive":            {Type: "boolean"},
		"salvage_truncated":           {Type: "boolean"},
		"async_scan":                  {Type: "boolean"},
		"step_transactions":           {Type: "boolean"},
		"recorded_concurrency":        {Type: "boolean"},
		"disable_salesforce":          {Type: "boolean"},
		"salesforce_native":           {Type: "boolean"},
		"agentforce_transform":        {Type: "boolean"},
		"filter_client_telemetry":     {Type: "boolean"},
		"parameterize":                {Type: "boolean"},
		"parameterization_selections": {Type: "array", Items: &object},
		"secret_bindings":             {Type: "array", Items: &object},
		"options":                     object,
	}
	if allowComparison {
		jsonProps["secondary_content"] = oasSchema{Type: "string", Description: "Optional second HAR JSON payload from an independent session for paired correlation classification."}
		jsonProps["comparison_content"] = oasSchema{Type: "string", Description: "Alias of secondary_content."}
		jsonProps["content_b"] = oasSchema{Type: "string", Description: "Alias of secondary_content."}
	}
	contract.RequestBody.Content["application/json"] = oasMediaType{
		Schema: oasSchema{
			Type:                 "object",
			Properties:           jsonProps,
			AdditionalProperties: boolPointer(true),
		},
	}
	contract.RequestBody.Content["application/octet-stream"] = oasMediaType{
		Schema: oasSchema{
			Type:        "string",
			Format:      "binary",
			Description: "Raw HAR JSON or single-HAR ZIP archive. Pass filename, project_id, validate_only, and conversion flags as query parameters.",
		},
	}
}

func negotiatedImportResponseComponent(path string) string {
	switch path {
	case "/api/v1/scripts/import/enterprise-script-archive", "/api/v1/scripts/import/enterprise-script-archive/validate":
		return "EnterpriseArchiveReviewResponse"
	case "/api/v1/recorder/{id}/convert", "/api/v1/browser-recorder/sessions/{id}/convert":
		return "SavedRecordingConversionResponse"
	case "/api/v1/scripts/import/sample", "/api/v1/scripts/import/har", "/api/v1/scripts/import/salesforce",
		"/api/v1/scripts/import/devweb", "/api/v1/scripts/import/k6", "/api/v1/scripts/import/github",
		"/api/v1/scripts/import/openapi", "/api/v1/scripts/import/raml", "/api/v1/scripts/import/wsdl",
		"/api/v1/scripts/import/postman", "/api/v1/scripts/import/postman/validate",
		"/api/v1/scripts/import/asyncapi", "/api/v1/scripts/import/eventhubs-capture", "/api/v1/scripts/import/broker-config",
		"/api/v1/scripts/import/jmeter", "/api/v1/scripts/import/jmeter/validate", "/api/v1/scripts/import/cloudtest",
		"/api/v1/scripts/import/recorder", "/api/v1/scripts/import/recorder/validate", "/api/v1/scripts/import/recorder/replay",
		"/api/v1/scripts/import/browser-recorder/attest", "/api/v1/recorder/{id}/convert/preview",
		"/api/v1/browser-recorder/sessions/{id}/convert/preview":
		return "ScriptImportReviewResponse"
	default:
		return ""
	}
}

func scriptImportReviewOpenAPISchemas() map[string]oasSchema {
	object := oasSchema{Type: "object", AdditionalProperties: boolPointer(true)}
	optionalObject := object
	optionalObject.Nullable = true
	schema := oasSchema{
		Type: "object", AdditionalProperties: boolPointer(true),
		Description: "Converted drafts and review facts. The default legacy representation includes script, which may be null. With response_format=canonical-v1, scripts is the only top-level draft collection and optional indexes and aliases identify convenience values without repeating them.",
		Properties: map[string]oasSchema{
			"script":               optionalObject,
			"scripts":              {Type: "array", Items: &object},
			"warnings":             {Type: "array", Items: &oasSchema{Type: "string"}},
			"summary":              object,
			"stats":                object,
			"result":               optionalObject,
			"diagnostics":          {Type: "array", Items: &object, Nullable: true},
			"unsupported_elements": {Type: "array", Items: &oasSchema{Type: "string"}},
			"batch":                object,
			"import_session_id":    {Type: "string"},
			"import_session_ids":   {Type: "array", Items: &oasSchema{Type: "string"}},
			"expires_at":           {Type: "string", Format: "date-time"},
			"replay_session_id":    {Type: "string"},
			"replay_session":       object,
			"http_script":          optionalObject,
			"scenario_drafts":      {Type: "array", Items: &object, Nullable: true},
			"data_pool_drafts":     {Type: "array", Items: &object, Nullable: true},
			"environment_drafts":   {Type: "array", Items: &object, Nullable: true},
			"monitor_drafts":       {Type: "array", Items: &object, Nullable: true},
			"preserved_artifacts":  {Type: "array", Items: &object, Nullable: true},
			"dispositions":         {Type: "array", Items: &object, Nullable: true},
			"source_retention":     object,
		},
		Required: []string{"script", "scripts", "warnings", "summary"},
	}
	applyCanonicalImportResponseSchema(&schema)
	saved := schema
	saved.Description = "Saved recording conversion. script and scripts contain StoredScript envelopes, not bare drafts. The required script alias remains present even when response_format is canonical-v1; optional http_script is a saved HTTP companion."
	saved.Required = append(append([]string(nil), schema.Required...), "script")
	saved.OneOf = nil
	saved.Properties = maps.Clone(schema.Properties)
	saved.Properties["script"] = object
	saved.Properties["primary_script_index"] = oasSchema{Type: "integer", Description: "Zero-based primary index retained when canonical negotiation was requested; the saved script alias remains present."}
	invalid := schema
	invalid.Description = "Converted recorder import retained for review after replay validation fails. The envelope follows the requested response format; valid is false and issues identifies the replay refusals."
	invalid.Properties = maps.Clone(schema.Properties)
	invalid.Properties["valid"] = oasSchema{Type: "boolean", Description: "Always false for this replay-validation refusal."}
	invalid.Properties["issues"] = oasSchema{Type: "array", Items: &oasSchema{Type: "string"}}
	invalid.Required = append(append([]string(nil), schema.Required...), "valid", "issues")
	return map[string]oasSchema{
		"ScriptImportReviewResponse": schema, "SavedRecordingConversionResponse": saved,
		"RecorderImportReplayInvalidResponse": invalid,
	}
}

// Keep the shared review fields on the enclosing schema. The oneOf retains the
// legacy required alias while admitting canonical responses without it, including
// empty drafts where neither primary nor preview has an index.
func applyCanonicalImportResponseSchema(schema *oasSchema) {
	if schema.Properties == nil {
		schema.Properties = map[string]oasSchema{}
	}
	format := oasSchema{Type: "string", Enum: []string{"canonical-v1"}, Description: "Present only for the negotiated canonical representation."}
	schema.Properties["response_format"] = format
	schema.Properties["primary_script_index"] = oasSchema{Type: "integer", Description: "Zero-based index into scripts for the primary draft. Omitted when no primary draft exists; the legacy script alias is absent in canonical responses."}
	schema.Properties["salesforce_preview_script_index"] = oasSchema{Type: "integer", Description: "Zero-based index into scripts replacing summary.salesforce_review.native_preview.script only when that preview is identical to a canonical draft. A distinct preview stays inline."}
	schema.Properties["result_aliases"] = oasSchema{Type: "object", AdditionalProperties: oasSchema{Type: "string"}, Description: "Maps omitted result field names to equal top-level response fields. Canonical result retains converter-specific facts; script and scripts aliases are omitted."}
	schema.Properties["unsupported_from_diagnostics"] = oasSchema{Type: "boolean", Description: "When true, unsupported_elements is omitted because its strings are exactly represented by the grouped diagnostics. Distinct unsupported notes remain inline."}
	required := make([]string, 0, len(schema.Required))
	for _, field := range schema.Required {
		if field != "script" {
			required = append(required, field)
		}
	}
	schema.Required = required
	schema.OneOf = []oasSchema{
		{Type: "object", Description: "Legacy response, including omitted or unrecognized response_format requests.", Properties: map[string]oasSchema{"script": schema.Properties["script"]}, Required: []string{"script"}},
		{Type: "object", Description: "Canonical response without the inline script alias.", Properties: map[string]oasSchema{"response_format": format}, Required: []string{"response_format"}},
	}
}
