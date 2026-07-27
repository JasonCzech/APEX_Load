package storage

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestGenericImportBatchRejectsCredentialsInExecutableConfigSurfaces(t *testing.T) {
	const canary = "batch-config-canary-921e"
	tests := []struct {
		name   string
		mutate func(*dsl.Script)
	}{
		{
			name: "network address URL",
			mutate: func(script *dsl.Script) {
				script.Config.Network.Address = "ldap://user:" + canary + "@directory.test"
			},
		},
		{
			name: "websocket origin URL",
			mutate: func(script *dsl.Script) {
				script.Config.WebSocket.Origin = "https://user:" + canary + "@origin.test"
			},
		},
		{
			name: "SOAP namespace userinfo",
			mutate: func(script *dsl.Script) {
				script.Config.SOAP.Namespaces = map[string]string{
					"auth": "https://user:" + canary + "@schemas.test/auth",
				}
			},
		},
		{
			name: "SOAP namespace attribute injection",
			mutate: func(script *dsl.Script) {
				script.Config.SOAP.Namespaces = map[string]string{
					"auth": `urn:auth" password="` + canary,
				}
			},
		},
		{
			name: "SOAP action userinfo",
			mutate: func(script *dsl.Script) {
				script.Actions[0].SOAP = &dsl.SOAPActionConfig{
					Action: "https://user:" + canary + "@service.test/action",
				}
			},
		},
		{
			name: "JMS broker URL",
			mutate: func(script *dsl.Script) {
				script.Config.JMS.BrokerURL = "amqps://user:" + canary + "@broker.test"
			},
		},
		{
			name: "JMS connection string",
			mutate: func(script *dsl.Script) {
				script.Config.JMS.BrokerURL = "Endpoint=sb://broker.test/;SharedAccessKey=" + canary
			},
		},
		{
			name: "Kafka schema registry URL",
			mutate: func(script *dsl.Script) {
				script.Config.Kafka.SchemaRegistryURL = "https://user:" + canary + "@registry.test"
			},
		},
		{
			name: "browser credential origin",
			mutate: func(script *dsl.Script) {
				script.Config.Browser.HTTPCredentials.Origin = "https://user:" + canary + "@browser.test"
			},
		},
		{
			name: "browser client certificate origin",
			mutate: func(script *dsl.Script) {
				script.Config.Browser.ClientCertificates = []dsl.BrowserClientCertificate{{
					Origin: "https://user:" + canary + "@browser.test",
				}}
			},
		},
		{
			name: "Java VM system property",
			mutate: func(script *dsl.Script) {
				script.Config.JavaVuser.AdditionalVMParameters = []string{
					"-Dclient.password=" + canary,
				}
			},
		},
		{
			name: "Java runner command",
			mutate: func(script *dsl.Script) {
				script.Config.JavaVuser.RunnerCommand = "/opt/runner --password=" + canary
			},
		},
		{
			name: "web runtime additional attribute",
			mutate: func(script *dsl.Script) {
				script.Config.WebRuntime.AdditionalAttributes = map[string]string{
					"api_key": canary,
				}
			},
		},
		{
			name: "web runtime data formatting option",
			mutate: func(script *dsl.Script) {
				script.Config.WebRuntime.DataFormatting.Chain = []dsl.DFETransform{{
					Type: "url",
					Options: map[string]string{
						"password": canary,
					},
				}}
			},
		},
		{
			name: "database secret field containing a DSN",
			mutate: func(script *dsl.Script) {
				script.Config.Database.DSNSecret = "postgres://user:" + canary + "@database.test/app"
			},
		},
		{
			name: "event hubs secret field containing a connection string",
			mutate: func(script *dsl.Script) {
				script.Config.EventHubs.ConnectionStringSecret =
					"Endpoint=sb://hub.test/;SharedAccessKey=" + canary
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native",
				"postman-native-commit-v1",
				"config-surface-"+strings.ReplaceAll(test.name, " ", "-"),
				"config-surface-sha",
			)
			test.mutate(commit.Scripts[0])

			_, err := store.CommitImportBatch(commit)
			if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
				t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("credential boundary disclosed rejected value: %v", err)
			}
			assertGenericImportBatchTablesEmpty(t, store)
		})
	}
}

func TestGenericImportBatchRejectsCredentialsInInlineFileDataSources(t *testing.T) {
	const canary = "batch-file-canary-6d91"
	tests := []struct {
		name   string
		source dsl.DataSource
	}{
		{
			name: "text JSON",
			source: dsl.DataSource{
				Name:        "request_body",
				Type:        "file",
				InlineData:  `{"password":"` + canary + `"}`,
				ContentType: "application/json",
			},
		},
		{
			name: "base64 form",
			source: dsl.DataSource{
				Name:        "request_body",
				Type:        "file",
				InlineData:  base64.StdEncoding.EncodeToString([]byte("api_key=" + canary)),
				Encoding:    "base64",
				ContentType: "application/x-www-form-urlencoded",
			},
		},
		{
			name: "sensitive filename",
			source: dsl.DataSource{
				Name:       "attachment",
				Type:       "file",
				FileName:   "password.txt",
				InlineData: canary,
			},
		},
		{
			name: "sensitive filename with malformed base64",
			source: dsl.DataSource{
				Name:       "attachment",
				Type:       "file",
				FileName:   "api_key.bin",
				InlineData: "not-base64!",
				Encoding:   "base64",
			},
		},
		{
			name: "ordinary filename with malformed base64",
			source: dsl.DataSource{
				Name:       "attachment",
				Type:       "file",
				FileName:   "payload.bin",
				InlineData: "password=" + canary + "!",
				Encoding:   "base64",
			},
		},
		{
			name: "sensitive filename with oversized base64",
			source: dsl.DataSource{
				Name:     "attachment",
				Type:     "file",
				FileName: "password.bin",
				InlineData: canary + strings.Repeat(
					"A",
					base64.StdEncoding.EncodedLen(credentialpolicy.DefaultLimits().MaxBytes)+1025-len(canary),
				),
				Encoding: "base64",
			},
		},
		{
			name: "ordinary oversized text JSON",
			source: dsl.DataSource{
				Name:        "request_body",
				Type:        "file",
				FileName:    "payload.json",
				InlineData:  `{"password":"` + canary + `","padding":"` + strings.Repeat("a", credentialpolicy.DefaultLimits().MaxBytes) + `"}`,
				Encoding:    "text",
				ContentType: "application/json",
			},
		},
		{
			name: "base64-tagged CSV sensitive column",
			source: dsl.DataSource{
				Name:           "rows",
				Type:           "csv",
				InlineData:     "password\n" + canary + "\n",
				Encoding:       "base64",
				FirstRowHeader: true,
			},
		},
		{
			name: "oversized CSV discovers sensitive header",
			source: dsl.DataSource{
				Name:           "rows",
				Type:           "csv",
				InlineData:     "password\n" + canary + strings.Repeat("a", credentialpolicy.DefaultLimits().MaxBytes) + "\n",
				FirstRowHeader: true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native",
				"postman-native-commit-v1",
				"file-source-"+strings.ReplaceAll(test.name, " ", "-"),
				"file-source-sha",
			)
			commit.Scripts[0].DataSources = []dsl.DataSource{test.source}

			_, err := store.CommitImportBatch(commit)
			if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
				t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("credential boundary disclosed rejected value: %v", err)
			}
			assertGenericImportBatchTablesEmpty(t, store)
		})
	}
}

func TestGenericImportBatchAcceptsOrdinaryInlineBinaryFile(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"ordinary-file-source",
		"ordinary-file-source-sha",
	)
	commit.Scripts[0].DataSources = []dsl.DataSource{{
		Name:        "logo",
		Type:        "file",
		FileName:    "logo.png",
		InlineData:  base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', 0, 1, 2}),
		Encoding:    "base64",
		ContentType: "image/png",
	}}

	if _, err := store.CommitImportBatch(commit); err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
}

func TestGenericImportBatchAcceptsDedicatedLogicalSecretReferences(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"dedicated-secret-reference-session",
		"dedicated-secret-reference-sha",
	)
	commit.Scripts[0].Config.Database.DSNSecret = "DATABASE_DSN"
	commit.Scripts[0].Config.Kafka.SASLPasswordSecret = "{{secret.KAFKA_PASSWORD}}"
	commit.Scripts[0].Config.EventHubs.ConnectionStringSecret = "EVENTHUBS_CONNECTION"

	if _, err := store.CommitImportBatch(commit); err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
}
