package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Readiness resolves required credentials without contacting a monitored target.
// The assigned collector remains responsible for remote authentication at Start.
func validateRequiredMonitorCredentials(ctx context.Context, store storage.RunMonitorStore, secrets secretstore.Store, project, scenario string) error {
	if scenario == "" {
		return nil
	}
	bindings, err := store.GetMonitorBindings(project, scenario)
	if err != nil {
		return err
	}
	sources, err := store.ListMonitorSources(project)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, binding := range bindings.Sources {
		if !binding.Required {
			continue
		}
		for _, source := range sources {
			if source.ID != binding.SourceID || source.CredentialRef == "" {
				continue
			}
			if secrets == nil {
				return errors.New("required monitor credential store unavailable")
			}
			raw, _, err := secrets.Get(ctx, project, source.CredentialRef)
			var credential collectoragent.Credentials
			if err != nil || len(raw) > 16<<10 || json.Unmarshal([]byte(raw), &credential) != nil || credential.Username == "" || ((source.Pack == "jmx" || source.Pack == "jdbc") && credential.Password == "") || (credential.Password == "" && credential.PrivateKey == "") {
				return errors.New("required monitor credential unavailable or invalid")
			}
		}
	}
	return nil
}
