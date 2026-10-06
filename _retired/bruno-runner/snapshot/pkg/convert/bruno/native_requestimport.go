package bruno

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// finalizeNativeRequestAction is the deliberately thin boundary between
// Bruno's source-specific effective-request construction and the shared
// request projection pipeline. Bruno remains responsible for OpenCollection
// inheritance, URL/template semantics, credential requirement identity, and
// JavaScript recognition. Once those decisions are complete, the common
// projector owns the HTTP-shaped DSL action.
//
// The two compatibility restorations below are intentional:
//   - Bruno's v1 public contract exposed a case-sensitive compatibility header
//     map before HeaderList became authoritative.
//   - Review-only auth modes retain their existing Bruno gate and DSL payload
//     rather than acquiring a second generic gate during this migration.
//
// Both can be removed only through an explicitly allowlisted Bruno contract
// revision.
func finalizeNativeRequestAction(
	scaffold dsl.Action,
	item CollectionItem,
	file sourceFile,
	bodyType string,
) (dsl.Action, requestimport.Report, error) {
	headers := make(requestimport.HeaderList, 0, len(scaffold.HeaderList))
	for index, header := range scaffold.HeaderList {
		headers = append(headers, requestimport.Header{
			Name:  header.Name,
			Value: header.Value,
			Source: requestimport.SourceRef{
				Format:  "bruno",
				Path:    file.Path,
				Pointer: fmt.Sprintf("/http/headers/%d", index),
				ItemID:  item.UID,
			},
		})
	}

	requestAuth, reviewOnlyAuth := nativeRequestImportAuth(scaffold.Auth, file.Path, item.UID)
	timeout := requestimport.Timeout{}
	if scaffold.TimeoutMs > 0 {
		timeout = requestimport.Timeout{
			Mode:         requestimport.TimeoutBounded,
			Milliseconds: scaffold.TimeoutMs,
		}
	}
	redirects := requestimport.Redirects{}
	if scaffold.MaxRedirects != nil {
		redirects = requestimport.Redirects{
			Mode: requestimport.RedirectLimited,
			Max:  *scaffold.MaxRedirects,
		}
	}

	effective := requestimport.EffectiveRequest{
		Key:     firstNonEmptyNative(item.UID, item.Path),
		Name:    scaffold.Name,
		Method:  scaffold.Method,
		Target:  scaffold.Target,
		Headers: headers,
		Body: requestimport.Body{
			Kind:     nativeRequestImportBodyKind(bodyType, scaffold.Body),
			Payload:  []byte(scaffold.Body),
			Encoding: requestimport.BodyEncodingText,
			Source: requestimport.SourceRef{
				Format:  "bruno",
				Path:    file.Path,
				Pointer: "/http/body",
				ItemID:  item.UID,
			},
		},
		Auth:      requestAuth,
		Timeout:   timeout,
		Redirects: redirects,
		Metadata:  scaffold.Metadata,
		Source: requestimport.SourceRef{
			Format:  "bruno",
			Path:    file.Path,
			Pointer: "/http",
			ItemID:  item.UID,
		},
	}
	action, report, err := requestimport.FinalizeAction(effective, requestimport.FinalizeOptions{
		Project: requestimport.ProjectOptions{
			// Bruno has already applied its immutable v1 clamp and diagnostic
			// rules above this boundary. Rejecting another out-of-range value
			// here makes any future divergence an invariant error.
			Bounds: requestimport.BoundsReject,
		},
		Overlay: requestimport.ActionOverlay{
			Type:           scaffold.Type,
			Params:         scaffold.Params,
			Metadata:       scaffold.Metadata,
			Extractors:     scaffold.Extractors,
			Assertions:     scaffold.Assertions,
			ThinkTime:      scaffold.ThinkTime,
			ThinkTimeAfter: scaffold.ThinkTimeAfter,
			SOAP:           scaffold.SOAP,
		},
		ActionPath: "actions[0]",
	})
	if err != nil {
		return dsl.Action{}, report, err
	}

	// Preserve the immutable Bruno-native-v1 compatibility map. HeaderList is
	// authoritative for replay and is projected entirely by requestimport.
	action.Headers = cloneNativeHeaderMap(scaffold.Headers)
	if reviewOnlyAuth != nil {
		authCopy := *reviewOnlyAuth
		action.Auth = &authCopy
	}
	return action, report, nil
}

func nativeRequestImportAuth(auth *dsl.AuthConfig, sourcePath, itemID string) (requestimport.Auth, *dsl.AuthConfig) {
	if auth == nil {
		return requestimport.Auth{}, nil
	}
	source := requestimport.SourceRef{
		Format:  "bruno",
		Path:    sourcePath,
		Pointer: "/http/auth",
		ItemID:  itemID,
	}
	projected := requestimport.Auth{
		Username: auth.Username,
		Password: auth.Password,
		Token:    auth.Token,
		Domain:   auth.Domain,
		Source:   source,
	}
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "basic":
		projected.Mode = requestimport.AuthBasic
	case "bearer":
		projected.Mode = requestimport.AuthBearer
	case "digest":
		projected.Mode = requestimport.AuthDigest
	default:
		// The existing Bruno review gate remains the durable execution policy.
		// Project this exceptional action without asking the generic layer to
		// manufacture a second public finding, then restore the authored mode.
		authCopy := *auth
		return requestimport.Auth{}, &authCopy
	}
	return projected, nil
}

func nativeRequestImportBodyKind(bodyType, body string) requestimport.BodyKind {
	bodyType = strings.ToLower(strings.TrimSpace(bodyType))
	switch {
	case body == "":
		return requestimport.BodyNone
	case bodyType == "json":
		return requestimport.BodyJSON
	case bodyType == "graphql":
		return requestimport.BodyGraphQL
	case strings.Contains(bodyType, "xml"), strings.HasPrefix(strings.TrimSpace(body), "<"):
		return requestimport.BodyXML
	case strings.Contains(bodyType, "form-urlencoded"), strings.Contains(bodyType, "urlencoded"):
		return requestimport.BodyFormURLEncoded
	case strings.Contains(bodyType, "multipart"):
		return requestimport.BodyMultipart
	default:
		return requestimport.BodyRaw
	}
}

func cloneNativeHeaderMap(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for name, value := range headers {
		cloned[name] = value
	}
	return cloned
}
