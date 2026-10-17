package recordbridge

import "github.com/jasonczech/apexload/pkg/config"

func normalizeProjectID(projectID string) string {
	if projectID == "" {
		return config.DefaultProjectID
	}
	return projectID
}
