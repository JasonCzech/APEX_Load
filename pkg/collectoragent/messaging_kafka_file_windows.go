package collectoragent

import (
	"errors"
	"os"
	"strings"
)

func openKafkaManagementTrustFile(path string) (*os.File, error) {
	// Reject device/UNC namespaces; trust is an approved local regular file.
	if strings.HasPrefix(path, `\\`) {
		return nil, errors.New("Kafka CA trust must be a local file")
	}
	return os.Open(path)
}
