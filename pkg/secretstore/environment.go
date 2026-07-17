package secretstore

import (
	"context"
	"os"

	"github.com/jasonczech/apexload/pkg/engine"
)

type EnvironmentStore struct{}

func (EnvironmentStore) Writable() bool   { return false }
func (EnvironmentStore) Provider() string { return "environment_read_only" }

func (EnvironmentStore) Get(ctx context.Context, projectID, name string) (string, Metadata, error) {
	if err := ctx.Err(); err != nil {
		return "", Metadata{}, err
	}
	projectID, name, err := validateIdentity(projectID, name)
	if err != nil {
		return "", Metadata{}, err
	}
	value, ok := os.LookupEnv(engine.ProjectSecretEnvironmentName(projectID, name))
	if !ok || !engine.IsUsableResolvedSecretValue(value) {
		return "", Metadata{}, ErrNotFound
	}
	return value, Metadata{
		ProjectID: projectID,
		Name:      name,
		Provider:  "environment_read_only",
		Writable:  false,
	}, nil
}

func (EnvironmentStore) Create(context.Context, string, string, string, string) (Metadata, error) {
	return Metadata{}, ErrReadOnly
}

func (EnvironmentStore) Replace(context.Context, string, string, string, string) (Metadata, error) {
	return Metadata{}, ErrReadOnly
}

func (EnvironmentStore) List(context.Context, string) ([]Metadata, error) {
	// Environment names are intentionally not enumerable; operators expose them
	// explicitly and imports probe only the requested project-scoped binding.
	return []Metadata{}, nil
}
