package importreview

import (
	"github.com/jasonczech/apexload/pkg/config"
	"strings"
)

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func NormalizeProjectID(projectID string) string {
	if projectID == "" {
		return config.DefaultProjectID
	}
	return projectID
}
