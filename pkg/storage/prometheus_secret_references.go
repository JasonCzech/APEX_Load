package storage

import "github.com/jasonczech/apexload/pkg/credential"

func prometheusSecretReferenceCount(snapshot *PrometheusConnectionSnapshot, name string) int {
	canonical, err := credential.NormalizeLogicalName(name)
	if snapshot == nil || err != nil {
		return 0
	}
	count := 0
	for _, reference := range []string{snapshot.TokenSecretName, snapshot.UsernameSecretName, snapshot.PasswordSecretName, snapshot.ClientSecretSecretName, snapshot.ClientCertificateSecretName, snapshot.ClientKeySecretName} {
		normalized, e := credential.NormalizeLogicalName(reference)
		if e == nil && normalized == canonical {
			count++
		}
	}
	return count
}

func preparationPrometheusSecretReferences(preparation *Preparation, name string) int {
	if preparation == nil || preparation.Monitors == nil {
		return 0
	}
	count := 0
	for _, connection := range preparation.Monitors.PrometheusConnections {
		count += prometheusSecretReferenceCount(connection, name)
	}
	return count
}
