package main

import (
	"context"
	"strings"
	"testing"
)

func TestRetiredBrunoCLIUsesNativeDefaultAndRejectsRunnerOptions(t *testing.T) {
	base := []string{"--api=https://example.test", "--path=collection", "--project=project", "--folder=folder"}
	args, err := parseBrunoImportArgs(base)
	if err != nil || args.output != "native" || args.sandbox != "" {
		t.Fatalf("args=%#v error=%v", args, err)
	}
	for _, extra := range [][]string{{"--output=canonical"}, {"--sync=legacy"}, {"--sandbox=safe"}, {"--conflict-resolution=field=use_source"}} {
		if _, err := parseBrunoImportArgs(append(append([]string{}, base...), extra...)); err == nil || !strings.Contains(err.Error(), "Bruno Runner has been retired") {
			t.Fatalf("options=%v error=%v", extra, err)
		}
	}
}

func TestRetiredBrunoCLIClientsRejectRunnerOptionsBeforeOpeningSource(t *testing.T) {
	var client *ApiClient
	for name, args := range map[string]brunoImportArgs{
		"canonical": {output: "canonical"},
		"sandbox":   {output: "native", sandbox: "safe"},
		"sync":      {output: "native", syncScriptID: "legacy"},
	} {
		t.Run(name, func(t *testing.T) {
			_, fileErr := client.InspectBrunoFile(context.Background(), args)
			_, directoryErr := client.InspectBrunoDirectory(context.Background(), args)
			for _, err := range []error{fileErr, directoryErr} {
				if err == nil || !strings.Contains(err.Error(), "Bruno Runner has been retired") {
					t.Fatalf("client retirement error = %v", err)
				}
			}
		})
	}
	if _, err := client.CommitBrunoImport(context.Background(), map[string]any{"mode": "create"}); err == nil || !strings.Contains(err.Error(), "Bruno Runner has been retired") {
		t.Fatalf("legacy commit error = %v", err)
	}
}
