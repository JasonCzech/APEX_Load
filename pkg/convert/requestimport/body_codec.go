package requestimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

func materializeEffectiveRequest(request Request) (Request, error) {
	projected := cloneEffectiveRequest(request)
	switch projected.Protocol {
	case "", ProtocolHTTP, ProtocolSOAP, ProtocolJavaHTTP:
	default:
		return Request{}, fmt.Errorf("unsupported effective request protocol %q", projected.Protocol)
	}
	switch projected.URLPolicy {
	case URLPolicyUnspecified, URLPolicyPreserveRaw, URLPolicyEncoded:
	default:
		return Request{}, fmt.Errorf("unsupported URL encoding policy %q", projected.URLPolicy)
	}
	body, err := materializeBody(projected.Body, requestIdentity(projected))
	if err != nil {
		return Request{}, err
	}
	projected.Body = body
	projected.MissingReferences = append(projected.MissingReferences, unresolvedBindingReferences(projected.Bindings)...)
	return projected, nil
}

func materializeBody(body Body, identity string) (Body, error) {
	structured := len(body.FormFields) > 0 || body.Multipart != nil || body.GraphQL != nil ||
		strings.TrimSpace(body.FileAssetRef) != ""
	if len(body.Payload) > 0 && structured {
		return Body{}, errors.New("body payload and structured body fields cannot both be set")
	}
	switch body.Kind {
	case BodyNone:
		if structured {
			return Body{}, errors.New("body kind is required when structured body fields are present")
		}
	case BodyFormURLEncoded:
		if len(body.Payload) == 0 && len(body.FormFields) > 0 {
			payload, err := serializeFormFields(body.FormFields)
			if err != nil {
				return Body{}, err
			}
			body.Payload = []byte(payload)
		}
		body.FormFields = nil
		if body.MediaType == "" {
			body.MediaType = "application/x-www-form-urlencoded"
		}
	case BodyMultipart:
		if body.Multipart != nil {
			var err error
			body, err = materializeMultipart(body, identity)
			if err != nil {
				return Body{}, err
			}
			body.Multipart = nil
		}
	case BodyGraphQL:
		if body.GraphQL != nil {
			payload, err := serializeGraphQL(*body.GraphQL)
			if err != nil {
				return Body{}, err
			}
			body.Payload = payload
			body.GraphQL = nil
		}
		if body.MediaType == "" {
			body.MediaType = "application/json"
		}
	case BodyFile:
		name := strings.TrimSpace(body.FileAssetRef)
		if name == "" {
			return Body{}, errors.New("file body requires file_asset_ref")
		}
		body.Payload = []byte("{{file." + name + "}}")
		body.Assets = ensureAsset(body.Assets, name)
		body.FileAssetRef = ""
	case BodyRaw, BodyJSON, BodyXML:
		if structured {
			return Body{}, fmt.Errorf("%s body cannot carry structured fields", body.Kind)
		}
	}
	return body, nil
}

func serializeFormFields(fields []FormField) (string, error) {
	var output strings.Builder
	for index, field := range fields {
		if index > 0 {
			output.WriteByte('&')
		}
		if field.RawName != "" {
			if err := validateRawFormComponent(field.RawName, true); err != nil {
				return "", fmt.Errorf("form field %d raw name: %w", index, err)
			}
			output.WriteString(field.RawName)
		} else {
			output.WriteString(url.QueryEscape(field.Name))
		}
		hadEquals := field.HadEquals == nil || *field.HadEquals
		if !hadEquals {
			continue
		}
		output.WriteByte('=')
		if field.RawValue != "" {
			if err := validateRawFormComponent(field.RawValue, false); err != nil {
				return "", fmt.Errorf("form field %d raw value: %w", index, err)
			}
			output.WriteString(field.RawValue)
		} else {
			output.WriteString(url.QueryEscape(field.Value))
		}
	}
	return output.String(), nil
}

func validateRawFormComponent(value string, name bool) error {
	if strings.ContainsAny(value, "\r\n&") || name && strings.Contains(value, "=") {
		return errors.New("contains an unencoded form delimiter")
	}
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}
		if index+2 >= len(value) || !isHex(value[index+1]) || !isHex(value[index+2]) {
			return errors.New("contains an invalid percent escape")
		}
		index += 2
	}
	return nil
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func materializeMultipart(body Body, identity string) (Body, error) {
	parts := body.Multipart.Parts
	boundary := strings.TrimSpace(body.Multipart.Boundary)
	mediaType := strings.TrimSpace(body.MediaType)
	mediaBoundary, err := multipartMediaBoundary(mediaType)
	if err != nil {
		return Body{}, err
	}
	if mediaBoundary != "" {
		if boundary != "" && boundary != mediaBoundary {
			return Body{}, errors.New("multipart body boundary conflicts with media type boundary")
		}
		boundary = mediaBoundary
	}
	if boundary == "" {
		boundary = deterministicMultipartBoundary(identity, parts, 0)
		for attempt := 1; multipartBoundaryOccurs(boundary, parts); attempt++ {
			boundary = deterministicMultipartBoundary(identity, parts, attempt)
		}
	}
	writer := multipart.NewWriter(io.Discard)
	if err := writer.SetBoundary(boundary); err != nil {
		return Body{}, fmt.Errorf("invalid multipart boundary: %w", err)
	}
	_ = writer.Close()

	var payload bytes.Buffer
	for index, part := range parts {
		if len(part.Data) > 0 && strings.TrimSpace(part.FileAssetRef) != "" {
			return Body{}, fmt.Errorf("multipart part %d has both inline data and a file asset", index)
		}
		headers := append(HeaderList(nil), part.Headers...)
		if !headers.Has("Content-Disposition") {
			if strings.TrimSpace(part.Name) == "" {
				return Body{}, fmt.Errorf("multipart part %d requires a name or Content-Disposition header", index)
			}
			disposition := "form-data; name=" + strconv.Quote(part.Name)
			if part.FileName != "" {
				disposition += "; filename=" + strconv.Quote(part.FileName)
			}
			headers = append(headers, Header{Name: "Content-Disposition", Value: disposition, Source: part.Source})
		}
		if part.ContentType != "" && !headers.Has("Content-Type") {
			headers = append(headers, Header{Name: "Content-Type", Value: part.ContentType, Source: part.Source})
		}
		for headerIndex, header := range headers {
			if !httpguts.ValidHeaderFieldName(header.Name) ||
				!httpguts.ValidHeaderFieldValue(header.Value) {
				return Body{}, fmt.Errorf("multipart part %d header %d is invalid", index, headerIndex)
			}
		}
		payload.WriteString("--")
		payload.WriteString(boundary)
		payload.WriteString("\r\n")
		for _, header := range headers {
			payload.WriteString(header.Name)
			payload.WriteString(": ")
			payload.WriteString(header.Value)
			payload.WriteString("\r\n")
		}
		payload.WriteString("\r\n")
		if asset := strings.TrimSpace(part.FileAssetRef); asset != "" {
			payload.WriteString("{{file.")
			payload.WriteString(asset)
			payload.WriteString("}}")
			body.Assets = ensureAsset(body.Assets, asset)
		} else {
			payload.Write(part.Data)
		}
		payload.WriteString("\r\n")
	}
	payload.WriteString("--")
	payload.WriteString(boundary)
	payload.WriteString("--\r\n")
	body.Payload = payload.Bytes()
	body.Multipart.Boundary = boundary
	if mediaType == "" {
		body.MediaType = "multipart/form-data; boundary=" + boundary
	} else {
		parsedType, parameters, _ := mime.ParseMediaType(mediaType)
		if strings.TrimSpace(parameters["boundary"]) == "" {
			parameters["boundary"] = boundary
			body.MediaType = mime.FormatMediaType(parsedType, parameters)
		}
	}
	return body, nil
}

func multipartMediaBoundary(mediaType string) (string, error) {
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		return "", nil
	}
	parsedType, parameters, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return "", fmt.Errorf("invalid multipart media type: %w", err)
	}
	if !strings.HasPrefix(strings.ToLower(parsedType), "multipart/") {
		return "", fmt.Errorf("multipart body has non-multipart media type %q", parsedType)
	}
	return strings.TrimSpace(parameters["boundary"]), nil
}

func serializeGraphQL(graphQL GraphQLBody) ([]byte, error) {
	if strings.TrimSpace(graphQL.Query) == "" && len(graphQL.Extensions) == 0 {
		return nil, errors.New("GraphQL body requires a query or extensions")
	}
	var output bytes.Buffer
	output.WriteByte('{')
	wrote := false
	appendString := func(name, value string, include bool) {
		if !include {
			return
		}
		if wrote {
			output.WriteByte(',')
		}
		key, _ := json.Marshal(name)
		encoded, _ := json.Marshal(value)
		output.Write(key)
		output.WriteByte(':')
		output.Write(encoded)
		wrote = true
	}
	appendRaw := func(name string, value json.RawMessage) error {
		if len(value) == 0 {
			return nil
		}
		if firstJSONByte(value) != '{' {
			return fmt.Errorf("GraphQL %s must be a JSON object", name)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, value); err != nil {
			return fmt.Errorf("GraphQL %s must contain one valid JSON value", name)
		}
		if wrote {
			output.WriteByte(',')
		}
		key, _ := json.Marshal(name)
		output.Write(key)
		output.WriteByte(':')
		output.Write(compact.Bytes())
		wrote = true
		return nil
	}
	appendString("query", graphQL.Query, graphQL.Query != "")
	appendString("operationName", graphQL.OperationName, graphQL.OperationName != "")
	if err := appendRaw("variables", graphQL.Variables); err != nil {
		return nil, err
	}
	if err := appendRaw("extensions", graphQL.Extensions); err != nil {
		return nil, err
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

func firstJSONByte(value []byte) byte {
	for _, current := range value {
		switch current {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return current
		}
	}
	return 0
}

func deterministicMultipartBoundary(identity string, parts []MultipartPart, attempt int) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("apexload-request-multipart-v1\x00"))
	_, _ = hash.Write([]byte(identity))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.Itoa(attempt)))
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part.Name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part.FileName))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(part.FileAssetRef))
	}
	return "----apexload-" + hex.EncodeToString(hash.Sum(nil)[:12])
}

func multipartBoundaryOccurs(boundary string, parts []MultipartPart) bool {
	needle := []byte(boundary)
	for _, part := range parts {
		if bytes.Contains(part.Data, needle) {
			return true
		}
	}
	return false
}

func ensureAsset(assets []Asset, name string) []Asset {
	for _, asset := range assets {
		if strings.TrimSpace(asset.Name) == name {
			return assets
		}
	}
	return append(assets, Asset{Name: name})
}

func requestIdentity(request Request) string {
	return firstNonEmpty(
		request.CandidateID,
		request.Key,
		request.Source.ItemID,
		sourceLocation(request.Source),
		request.Name,
	)
}

func unresolvedBindingReferences(bindings []SourceBinding) []MissingReference {
	var refs []MissingReference
	for _, binding := range bindings {
		if binding.Resolved {
			continue
		}
		if name := strings.TrimSpace(binding.Name); name != "" {
			refs = append(refs, MissingReference{
				Namespace: binding.Namespace,
				Name:      name,
				Source:    binding.Source,
			})
		}
	}
	return refs
}
