package dslwalk

import (
	"encoding/json"
	"fmt"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// StringField describes an authored scalar without conflating absence, empty
// text, and malformed JSON-shaped input. Raw is retained for diagnostics or an
// explicit repair; reading a field never rewrites the source representation.
// Ordinary typed string fields cannot represent explicit JSON absence versus
// empty text, so Present follows the DSL's omitempty contract. Optional fields
// such as HTTPVersion retain their own presence independently.
type StringField struct {
	Value   string
	Raw     any
	Present bool
	Valid   bool
}

func stringField(value any, present bool) StringField {
	if !present {
		return StringField{Valid: true}
	}
	text, ok := value.(string)
	return StringField{Value: text, Raw: value, Present: true, Valid: ok}
}

func typedStringField(value string) StringField {
	return StringField{Value: value, Raw: value, Present: value != "", Valid: true}
}

func (ref SurfaceRef) Target() StringField {
	if ref.Action != nil {
		return typedStringField(ref.Action.Target)
	}
	value, present := ref.Request["target"]
	return stringField(value, present)
}
func (ref SurfaceRef) Body() StringField {
	if ref.Action != nil {
		return typedStringField(ref.Action.Body)
	}
	value, present := ref.Request["body"]
	return stringField(value, present)
}
func (ref SurfaceRef) BodyEncoding() StringField {
	if ref.Action != nil {
		return typedStringField(ref.Action.BodyEncoding)
	}
	value, present := ref.Request["body_encoding"]
	return stringField(value, present)
}
func (ref SurfaceRef) HTTPVersion() StringField {
	if ref.Action != nil {
		version := ref.Action.HTTPVersion
		if !version.IsPresent() {
			return StringField{Valid: true}
		}
		encoded, _ := json.Marshal(version)
		var raw any
		_ = json.Unmarshal(encoded, &raw)
		return stringField(raw, true)
	}
	value, present := ref.Request["http_version"]
	return stringField(value, present)
}

// SetTarget, SetBody, and SetBodyEncoding preserve explicit empty map scalars.
// ClearBodyEncoding is separate because deleting an inherited encoding is an
// authoring decision, rather than a consequence of trimming an empty value.
func (ref SurfaceRef) SetTarget(value string) {
	if ref.Action != nil {
		ref.Action.Target = value
	} else if ref.Request != nil {
		ref.Request["target"] = value
	}
}
func (ref SurfaceRef) SetBody(value string) {
	if ref.Action != nil {
		ref.Action.Body = value
	} else if ref.Request != nil {
		ref.Request["body"] = value
	}
}
func (ref SurfaceRef) SetBodyEncoding(value string) {
	if ref.Action != nil {
		ref.Action.BodyEncoding = value
	} else if ref.Request != nil {
		ref.Request["body_encoding"] = value
	}
}
func (ref SurfaceRef) ClearBodyEncoding() {
	if ref.Action != nil {
		ref.Action.BodyEncoding = ""
	} else {
		delete(ref.Request, "body_encoding")
	}
}
func (ref SurfaceRef) SetHTTPVersion(value string) error {
	version, err := dsl.NewHTTPVersionOverride(value)
	if err != nil {
		return err
	}
	if ref.Action != nil {
		ref.Action.HTTPVersion = version
	} else if ref.Request != nil {
		ref.Request["http_version"] = value
	} else {
		return fmt.Errorf("request surface is absent")
	}
	return nil
}
func (ref SurfaceRef) ClearHTTPVersion() {
	if ref.Action != nil {
		ref.Action.HTTPVersion = dsl.OptionalHTTPVersion{}
	} else {
		delete(ref.Request, "http_version")
	}
}

// HeaderFields keeps the ordered list and legacy compatibility map independent.
// Raw values deliberately retain malformed entries, unknown fields, duplicate
// occurrences, disabled entries, and explicit null/empty containers. Parsing,
// repair, precedence, and compatibility-map policy belong to the consumer.
type HeaderFields struct {
	List        any
	ListPresent bool
	Map         any
	MapPresent  bool
}

func (ref SurfaceRef) Headers() HeaderFields {
	if ref.Action != nil {
		return HeaderFields{List: ref.Action.HeaderList, ListPresent: ref.Action.HeaderList != nil, Map: ref.Action.Headers, MapPresent: ref.Action.Headers != nil}
	}
	list, lp := ref.Request["header_list"]
	headers, hp := ref.Request["headers"]
	return HeaderFields{List: list, ListPresent: lp, Map: headers, MapPresent: hp}
}

// SetHeaders installs both representations supplied by the transformation.
// It does not infer a lossy map from an ordered list or merge authored values.
func (ref SurfaceRef) SetHeaders(list dsl.HeaderList, compatibility map[string]string) {
	if ref.Action != nil {
		ref.Action.HeaderList, ref.Action.Headers = list, compatibility
		return
	}
	if ref.Request != nil {
		ref.Request["header_list"], ref.Request["headers"] = list, compatibility
	}
}
func (ref SurfaceRef) ClearHeaders() {
	if ref.Action != nil {
		ref.Action.HeaderList, ref.Action.Headers = nil, nil
		return
	}
	delete(ref.Request, "header_list")
	delete(ref.Request, "headers")
}
