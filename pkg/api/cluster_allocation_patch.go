package api

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

// defaultGeneratorContainerName is the container both shipped manifests use
// (deployments/k8s/aks/generator-statefulset.yaml and the Helm chart). The Helm
// chart also runs a java-vuser-runner sidecar in the same pod, which is exactly
// why the container is selected by name and never by index.
const defaultGeneratorContainerName = "generator"

// errAmbiguousGeneratorContainer is returned when the target container cannot
// be identified with certainty. Guessing here would resize the wrong container.
type errAmbiguousGeneratorContainer struct {
	Names []string
}

func (e *errAmbiguousGeneratorContainer) Error() string {
	return fmt.Sprintf("cannot determine which container to resize (found %s); set the connection's generator container name",
		strings.Join(e.Names, ", "))
}

// resolveGeneratorContainer picks the container an allocation patch applies to,
// strictest signal first. It never falls back to "the first container".
func resolveGeneratorContainer(set kubeops.StatefulSetRef, configured string) (string, error) {
	names := make([]string, 0, len(set.Containers))
	for _, container := range set.Containers {
		names = append(names, container.Name)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("statefulset %q has no containers", set.Name)
	}
	if explicit := strings.TrimSpace(configured); explicit != "" {
		for _, name := range names {
			if name == explicit {
				return name, nil
			}
		}
		return "", fmt.Errorf("statefulset %q has no container named %q (found %s)", set.Name, explicit, strings.Join(names, ", "))
	}
	for _, name := range names {
		if name == defaultGeneratorContainerName {
			return name, nil
		}
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", &errAmbiguousGeneratorContainer{Names: names}
}

// buildAllocationPatch produces the strategic-merge patch for an allocation.
//
// Two Kubernetes merge semantics drive the shape:
//   - containers has patchMergeKey "name", so naming the container merges into
//     it and leaves sidecars untouched.
//   - nodeSelector is a MAP, so strategic merge only adds and overwrites keys.
//     A key the administrator removed would otherwise stay on the pod forever,
//     so removed keys must be sent as an explicit null.
//
// tolerations has no merge key, so the list is replaced wholesale — an empty
// slice must be sent (not omitted) to clear it.
func buildAllocationPatch(set kubeops.StatefulSetRef, containerName string, allocation *storage.PoolAllocation) map[string]any {
	requests := map[string]any{}
	limits := map[string]any{}
	if allocation != nil {
		putQuantity(requests, "cpu", allocation.CPURequest)
		putQuantity(requests, "memory", allocation.MemoryRequest)
		putQuantity(limits, "cpu", allocation.CPULimit)
		putQuantity(limits, "memory", allocation.MemoryLimit)
	}

	desiredSelector := allocation.EffectiveNodeSelector()
	nodeSelector := make(map[string]any, len(desiredSelector)+len(set.NodeSelector))
	for key, value := range desiredSelector {
		nodeSelector[key] = value
	}
	for key := range set.NodeSelector {
		if _, keep := desiredSelector[key]; !keep {
			// Explicit null is the only way strategic merge removes a map key.
			nodeSelector[key] = nil
		}
	}

	tolerations := make([]map[string]any, 0, len(allocationTolerations(allocation)))
	for _, toleration := range allocationTolerations(allocation) {
		entry := map[string]any{}
		if key := strings.TrimSpace(toleration.Key); key != "" {
			entry["key"] = key
		}
		if operator := strings.TrimSpace(toleration.Operator); operator != "" {
			entry["operator"] = operator
		}
		if value := strings.TrimSpace(toleration.Value); value != "" {
			entry["value"] = value
		}
		if effect := strings.TrimSpace(toleration.Effect); effect != "" {
			entry["effect"] = effect
		}
		tolerations = append(tolerations, entry)
	}

	container := map[string]any{"name": containerName}
	resources := map[string]any{}
	// Send an empty map rather than omitting so clearing a previously-set
	// request or limit actually takes effect.
	resources["requests"] = requests
	resources["limits"] = limits
	container["resources"] = resources

	return map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers":   []map[string]any{container},
					"nodeSelector": nodeSelector,
					"tolerations":  tolerations,
				},
			},
		},
	}
}

func allocationTolerations(allocation *storage.PoolAllocation) []storage.Toleration {
	if allocation == nil {
		return nil
	}
	return allocation.Tolerations
}

func putQuantity(target map[string]any, key, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		target[key] = trimmed
	}
}

// allocationTargetWarnings surfaces conditions that do not block the patch but
// change what the administrator should expect afterwards.
func allocationTargetWarnings(set kubeops.StatefulSetRef) []string {
	warnings := make([]string, 0, 2)
	if _, managed := set.Annotations["app.kubernetes.io/managed-by"]; managed {
		warnings = append(warnings,
			"this workload is managed by Helm; the next chart sync will revert allocation changes made here")
	}
	if _, tracked := set.Annotations["argocd.argoproj.io/tracking-id"]; tracked {
		warnings = append(warnings,
			"this workload is tracked by Argo CD; the next sync will revert allocation changes made here")
	}
	if set.UpdateStrategy == "OnDelete" {
		warnings = append(warnings,
			"the workload uses the OnDelete update strategy; existing generator pods keep their current resources until they are deleted")
	}
	return warnings
}

// verifyGeneratorWorkloadOwnership refuses to patch a StatefulSet that does not
// look like an APEX Load generator. Without this, a mistyped workload name
// would let an administrator resize an unrelated co-tenant workload in the same
// namespace.
func verifyGeneratorWorkloadOwnership(set kubeops.StatefulSetRef, selector string) error {
	requirements, err := parseLabelSelector(selector)
	if err != nil {
		return err
	}
	if len(requirements) == 0 {
		return nil
	}
	for key, want := range requirements {
		if strings.TrimSpace(set.PodTemplateJSON[key]) != want {
			return fmt.Errorf("statefulset %q does not match the connection's generator selector (%s); allocation refused",
				set.Name, selector)
		}
	}
	return nil
}

// parseLabelSelector understands the comma-separated key=value form used by
// the controller's generator selector configuration.
func parseLabelSelector(selector string) (map[string]string, error) {
	trimmed := strings.TrimSpace(selector)
	if trimmed == "" {
		return nil, nil
	}
	requirements := make(map[string]string)
	for _, part := range strings.Split(trimmed, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, found := strings.Cut(part, "=")
		if !found {
			return nil, fmt.Errorf("generator selector %q must use key=value pairs", selector)
		}
		requirements[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return requirements, nil
}
