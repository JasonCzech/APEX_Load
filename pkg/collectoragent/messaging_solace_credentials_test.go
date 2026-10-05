package collectoragent

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSolaceBasicAuthorizationExactPurposeAndPrincipal(t *testing.T) {
	target := solaceConfigTestTarget(t)
	credentials := ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "basic", Basic: &ManagementBasicCredentials{Username: "reader", Password: "password:with:separator"}}
	header, err := solaceBasicAuthorization(credentials, target.Identity)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(header)
	if string(header) != "Basic cmVhZGVyOnBhc3N3b3JkOndpdGg6c2VwYXJhdG9y" {
		t.Fatal("Basic wire differs from literal UTF8 identity/password")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(string(header), "Basic "))
	if err != nil || string(raw) != "reader:password:with:separator" {
		t.Fatal("Basic credentials changed")
	}
	clear(raw)
	for name, change := range map[string]func(*ManagementCredentials){
		"purpose":             func(c *ManagementCredentials) { c.Purpose = "messaging" },
		"version":             func(c *ManagementCredentials) { c.Version = 1 },
		"wrong principal":     func(c *ManagementCredentials) { c.Basic.Username = "admin" },
		"separator principal": func(c *ManagementCredentials) { c.Basic.Username = "reader:admin" },
		"empty password":      func(c *ManagementCredentials) { c.Basic.Password = "" },
		"empty user":          func(c *ManagementCredentials) { c.Basic.Username = "" },
		"oversized password":  func(c *ManagementCredentials) { c.Basic.Password = strings.Repeat("x", 8193) },
		"union": func(c *ManagementCredentials) {
			c.SharedKey = &ManagementSharedKeyCredentials{KeyName: "reader", Key: "key"}
		},
		"mode": func(c *ManagementCredentials) { c.Mode = "shared_key" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := credentials
			value := *credentials.Basic
			copy.Basic = &value
			change(&copy)
			header, err := solaceBasicAuthorization(copy, target.Identity)
			if err == nil || len(header) != 0 {
				t.Fatal("invalid credentials produced target authorization")
			}
		})
	}
	target.Identity.Principal = "reader:admin"
	credentials.Basic.Username = "reader:admin"
	if _, err := solaceBasicAuthorization(credentials, target.Identity); err == nil {
		t.Fatal("matching colon principal bypassed wire identity check")
	}
}
