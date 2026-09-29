package broker

import (
	"encoding/json"
	"math/big"
	"reflect"
	"regexp"
)

// Defaulting reference: https://github.com/kubernetes/kubernetes/blob/v1.34.3/pkg/apis/core/v1/defaults.go
// Admission may default harmless fields, but cannot alter an execution host's
// approved identity, isolation, assets, credentials, placement or reservations.
// Unknown fields fail closed until explicitly reviewed against the pod contract.
func verifyAdmittedPod(expected, observed []byte, scheduled bool) error {
	var want, got map[string]any
	if json.Unmarshal(expected, &want) != nil || json.Unmarshal(observed, &got) != nil {
		return ErrState
	}
	for _, key := range []string{"apiVersion", "kind"} {
		if want[key] != got[key] {
			return ErrState
		}
	}
	a, ok := want["spec"].(map[string]any)
	if !ok {
		return ErrState
	}
	b, ok := got["spec"].(map[string]any)
	if !ok {
		return ErrState
	}
	defaults := map[string]any{"dnsPolicy": "ClusterFirst", "schedulerName": "default-scheduler", "priority": float64(0), "preemptionPolicy": "PreemptLowerPriority", "serviceAccount": a["serviceAccountName"]}
	for key, value := range defaults {
		if actual, exists := b[key]; exists {
			if !reflect.DeepEqual(actual, value) {
				return ErrState
			}
			delete(b, key)
		}
	}
	if node, exists := b["nodeName"]; exists {
		value, ok := node.(string)
		if !ok || value != "" && (!scheduled || len(value) > 253) {
			return ErrState
		}
		delete(b, "nodeName")
	}
	if raw, exists := b["imagePullSecrets"]; exists {
		refs, ok := raw.([]any)
		if !ok || len(refs) > 16 {
			return ErrState
		}
		for _, raw := range refs {
			ref, ok := raw.(map[string]any)
			if !ok || len(ref) != 1 {
				return ErrState
			}
			name, ok := ref["name"].(string)
			if !ok || !namespaceName.MatchString(name) {
				return ErrState
			}
		}
		delete(b, "imagePullSecrets")
	}
	if raw, exists := b["tolerations"]; exists {
		list, ok := raw.([]any)
		if !ok || len(list) > 2 {
			return ErrState
		}
		seen := map[string]bool{}
		for _, raw := range list {
			v, ok := raw.(map[string]any)
			if !ok {
				return ErrState
			}
			key, ok := v["key"].(string)
			if !ok || seen[key] || key != "node.kubernetes.io/not-ready" && key != "node.kubernetes.io/unreachable" || !reflect.DeepEqual(v, map[string]any{"key": key, "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": float64(300)}) {
				return ErrState
			}
			seen[key] = true
		}
		delete(b, "tolerations")
	}
	// These false fields use API omitempty; absence has exactly the same meaning.
	for _, key := range []string{"hostNetwork", "hostPID", "hostIPC", "shareProcessNamespace"} {
		if _, ok := b[key]; !ok {
			b[key] = false
		}
	}
	for _, spec := range []map[string]any{a, b} {
		containers, ok := spec["containers"].([]any)
		if !ok || len(containers) != 1 {
			return ErrState
		}
		c, ok := containers[0].(map[string]any)
		if !ok {
			return ErrState
		}
		for key, value := range map[string]any{"terminationMessagePath": "/dev/termination-log", "terminationMessagePolicy": "File"} {
			if actual, exists := c[key]; exists {
				if !reflect.DeepEqual(actual, value) {
					return ErrState
				}
				delete(c, key)
			}
		}
		for _, key := range []string{"stdin", "stdinOnce", "tty"} {
			if actual, exists := c[key]; exists {
				if actual != false {
					return ErrState
				}
				delete(c, key)
			}
		}
		resources, ok := c["resources"].(map[string]any)
		if !ok {
			return ErrState
		}
		for _, kind := range []string{"requests", "limits"} {
			values, ok := resources[kind].(map[string]any)
			if !ok {
				return ErrState
			}
			for _, key := range []string{"cpu", "memory", "ephemeral-storage"} {
				value, ok := values[key].(string)
				if !ok {
					return ErrState
				}
				canonical, ok := canonicalPodQuantity(value)
				if !ok {
					return ErrState
				}
				values[key] = canonical
			}
		}
		volumes, ok := spec["volumes"].([]any)
		if !ok {
			return ErrState
		}
		for _, raw := range volumes {
			volume, ok := raw.(map[string]any)
			if !ok {
				return ErrState
			}
			if raw, exists := volume["emptyDir"]; exists {
				empty, ok := raw.(map[string]any)
				if !ok {
					return ErrState
				}
				value, ok := empty["sizeLimit"].(string)
				if !ok {
					return ErrState
				}
				canonical, ok := canonicalPodQuantity(value)
				if !ok {
					return ErrState
				}
				empty["sizeLimit"] = canonical
			}
		}
	}
	if !reflect.DeepEqual(a, b) {
		return ErrState
	}
	wm, ok := want["metadata"].(map[string]any)
	if !ok {
		return ErrState
	}
	gm, ok := got["metadata"].(map[string]any)
	if !ok {
		return ErrState
	}
	for _, key := range []string{"name", "namespace", "labels", "annotations"} {
		if !reflect.DeepEqual(wm[key], gm[key]) {
			return ErrState
		}
	}
	wa, ok := wm["annotations"].(map[string]any)
	if !ok {
		return ErrState
	}
	ga, ok := gm["annotations"].(map[string]any)
	if !ok {
		return ErrState
	}
	for _, key := range []string{hostAnnotation, epochAnnotation} {
		if ga[key] != wa[key] {
			return ErrState
		}
	}
	if value, exists := gm["deletionTimestamp"]; exists && value != nil {
		return ErrState
	}
	// Controller owners could replace a terminated JVM despite restartPolicy Never.
	for _, key := range []string{"ownerReferences", "finalizers"} {
		if raw, exists := gm[key]; exists && raw != nil {
			refs, ok := raw.([]any)
			if !ok || len(refs) != 0 {
				return ErrState
			}
		}
	}
	return nil
}

var podQuantity = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})(m|Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)?$`)

func canonicalPodQuantity(raw string) (string, bool) {
	parts := podQuantity.FindStringSubmatch(raw)
	if parts == nil {
		return "", false
	}
	value, ok := new(big.Int).SetString(parts[1], 10)
	if !ok {
		return "", false
	}
	scale := map[string]int{"Ki": 10, "Mi": 20, "Gi": 30, "Ti": 40, "Pi": 50, "Ei": 60}
	decimal := map[string]int64{"k": 3, "M": 6, "G": 9, "T": 12, "P": 15, "E": 18}
	denominator := big.NewInt(1)
	if parts[2] == "m" {
		denominator.SetInt64(1000)
	} else if n, ok := scale[parts[2]]; ok {
		value.Lsh(value, uint(n))
	} else if n, ok := decimal[parts[2]]; ok {
		value.Mul(value, new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil))
	}
	return new(big.Rat).SetFrac(value, denominator).RatString(), true
}
