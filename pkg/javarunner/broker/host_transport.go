package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func transportRecord(ctx context.Context, store *Store, epoch, id, namespace string) (Record, error) {
	if store == nil || store.config.Namespace != namespace {
		return Record{}, ErrState
	}
	records, _, e := store.Snapshot(ctx, epoch)
	if e != nil {
		return Record{}, e
	}
	for _, r := range records {
		if r.ID == id {
			return r, nil
		}
	}
	return Record{}, ErrState
}

// ProvisionHostTransport is the optional operator-owned PodConfig step before
// Provision. It creates once; conflicts/ambiguous responses are errors, never
// permission to adopt or overwrite credentials. Use material.SecretName in the
// pod config and material.ServerName/ServerURI for the broker TLS client.
// The caller must abort preparation on error and periodically reconcile the
// deterministic Secret name even for reaped records (including late creates).
func (k *KubernetesBackend) ProvisionHostTransport(ctx context.Context, store *Store, epoch, id string, material *HostTransportMaterial) error {
	r, e := transportRecord(ctx, store, epoch, id, k.namespace)
	if e != nil {
		return e
	}
	if r.BrokerEpoch != epoch || r.State != javarunner.HostReserved || material == nil || !material.expires.After(r.PrepareUntil) {
		return ErrState
	}
	body, e := material.SecretJSON(k.namespace, r)
	if e != nil {
		return e
	}
	data, status, e := k.resourceRequest(ctx, http.MethodPost, "secrets", "", body)
	if e != nil || status != http.StatusCreated {
		return ErrCleanup
	}
	var expected, received transportSecret
	if json.Unmarshal(body, &expected) != nil || json.Unmarshal(data, &received) != nil || !validTransportSecret(received, k.namespace, r) || !received.Immutable || received.Type != expected.Type || len(received.Data) != len(expected.Data) {
		return ErrCleanup
	}
	for key, value := range expected.Data {
		if !bytes.Equal(value, received.Data[key]) {
			return ErrCleanup
		}
	}
	// Cancellation/expiry after a successful create cannot authorize a pod.
	r, e = transportRecord(ctx, store, epoch, id, k.namespace)
	if e != nil || r.BrokerEpoch != epoch || r.State != javarunner.HostReserved {
		return ErrFence
	}
	return nil
}

type transportSecret struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name, Namespace, UID string
		Annotations          map[string]string
	}
	Type      string            `json:"type"`
	Immutable bool              `json:"immutable"`
	Data      map[string][]byte `json:"data"`
}

func validTransportSecret(s transportSecret, namespace string, r Record) bool {
	return s.APIVersion == "v1" && s.Kind == "Secret" && s.Metadata.Name == hostTransportName(r) && s.Metadata.Namespace == namespace && name.MatchString(s.Metadata.UID) && s.Metadata.Annotations[hostAnnotation] == r.ID && s.Metadata.Annotations[epochAnnotation] == r.BrokerEpoch
}

// ReconcileHostTransport removes only broker-generated transport after durable
// host termination (or abort before any pod-create intent). A Secret deletion
// cannot release capacity or prove termination. Call repeatedly for reaped
// tombstones: an ambiguous Secret create may arrive after an earlier NotFound.
func (k *KubernetesBackend) ReconcileHostTransport(ctx context.Context, store *Store, epoch, id string) error {
	r, e := transportRecord(ctx, store, epoch, id, k.namespace)
	if e != nil {
		return e
	}
	if r.State != javarunner.HostReaped {
		return ErrCleanup
	}
	data, status, e := k.resourceRequest(ctx, http.MethodGet, "secrets", hostTransportName(r), nil)
	if e != nil {
		return ErrCleanup
	}
	if status == http.StatusNotFound {
		return nil
	}
	var secret transportSecret
	if status != http.StatusOK || json.Unmarshal(data, &secret) != nil || !validTransportSecret(secret, k.namespace, r) {
		return ErrCleanup
	}
	body, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": secret.Metadata.UID}})
	_, status, e = k.resourceRequest(ctx, http.MethodDelete, "secrets", hostTransportName(r), body)
	if e != nil || status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNotFound {
		return ErrCleanup
	}
	return nil
}
