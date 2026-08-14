package credentialpolicy

import (
	"bytes"
	"strings"
	"testing"
)

func TestPreserveProjectionRejectsRedactionPlaceholderAcrossCredentialTransports(t *testing.T) {
	t.Parallel()
	const sentinel = "%255BReDaCtEd%255D"

	options := func() Options {
		return Options{
			Mode:       Preserve,
			Source:     SourceRef{Format: "fixture", Path: "source.bundle", Pointer: "/request"},
			RequestKey: "request-one",
			NamePrefix: "fixture",
			Registry:   NewNameRegistry(),
		}
	}
	tests := []struct {
		name    string
		project func(t *testing.T) (string, Result)
		want    int
	}{
		{
			name: "URL userinfo and query",
			want: 2,
			project: func(t *testing.T) (string, Result) {
				t.Helper()
				output, result, err := ProjectRawURL(
					"https://operator:"+sentinel+"@example.test/orders?token="+sentinel,
					options(),
				)
				if err != nil {
					t.Fatal(err)
				}
				return output, result
			},
		},
		{
			name: "typed auth password and token",
			want: 2,
			project: func(t *testing.T) (string, Result) {
				t.Helper()
				output, result, err := ProjectAuth(AuthFields{
					Type:     "digest",
					Username: "operator",
					Password: sentinel,
					Token:    sentinel,
				}, options())
				if err != nil {
					t.Fatal(err)
				}
				return output.Password + "\n" + output.Token, result
			},
		},
		{
			name: "JSON body",
			want: 1,
			project: func(t *testing.T) (string, Result) {
				t.Helper()
				output, result, err := ProjectRequestBody(
					[]byte(`{"password":"`+sentinel+`","ordinary":"keep"}`),
					"application/json",
					options(),
				)
				if err != nil {
					t.Fatal(err)
				}
				return string(output), result
			},
		},
		{
			name: "XML body",
			want: 2,
			project: func(t *testing.T) (string, Result) {
				t.Helper()
				output, result, err := ProjectRequestBody(
					[]byte(`<root password="`+sentinel+`"><password>`+sentinel+`</password><ordinary>keep</ordinary></root>`),
					"application/xml",
					options(),
				)
				if err != nil {
					t.Fatal(err)
				}
				return string(output), result
			},
		},
		{
			name: "form body",
			want: 1,
			project: func(t *testing.T) (string, Result) {
				t.Helper()
				output, result, err := ProjectRequestBody(
					[]byte("password="+sentinel+"&ordinary=keep"),
					"application/x-www-form-urlencoded",
					options(),
				)
				if err != nil {
					t.Fatal(err)
				}
				return string(output), result
			},
		},
		{
			name: "multipart body",
			want: 1,
			project: func(t *testing.T) (string, Result) {
				t.Helper()
				const boundary = "transport-matrix-boundary"
				payload := "--" + boundary + "\r\n" +
					"Content-Disposition: form-data; name=\"password\"\r\n\r\n" +
					sentinel + "\r\n" +
					"--" + boundary + "--\r\n"
				output, result, err := ProjectRequestBody(
					[]byte(payload),
					"multipart/form-data; boundary="+boundary,
					options(),
				)
				if err != nil {
					t.Fatal(err)
				}
				return string(output), result
			},
		},
		{
			name: "GraphQL variables",
			want: 1,
			project: func(t *testing.T) (string, Result) {
				t.Helper()
				output, result, err := ProjectGraphQL(GraphQL{
					Query: []byte(`mutation Login($password: String!) { login(password: $password) }`),
					Variables: []byte(
						`{"password":"` + sentinel + `","ordinary":"keep"}`,
					),
				}, options())
				if err != nil {
					t.Fatal(err)
				}
				return string(bytes.Join([][]byte{output.Query, output.Variables}, []byte("\n"))), result
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output, result := test.project(t)
			if result.Disposition != BlockedDraft {
				t.Fatalf("disposition = %q, want %q; result=%#v", result.Disposition, BlockedDraft, result)
			}
			if len(result.Requirements) != test.want {
				t.Fatalf("requirements = %#v, want %d unavailable bindings", result.Requirements, test.want)
			}
			for _, requirement := range result.Requirements {
				if requirement.SourceValueAvailable || requirement.Value != "" ||
					requirement.SourceValueStatus != SourceCredentialValueRedactedPlaceholder {
					t.Errorf("requirement retained placeholder material: %#v", requirement)
				}
				if !strings.HasPrefix(requirement.Replacement, "{{secret.") ||
					!strings.Contains(output, requirement.Replacement) {
					t.Errorf("requirement replacement is not present in projected transport: %#v output=%q", requirement, output)
				}
			}
			if strings.Contains(strings.ToLower(output), strings.ToLower(sentinel)) {
				t.Fatalf("projected transport retained source sentinel: %q", output)
			}
		})
	}
}
