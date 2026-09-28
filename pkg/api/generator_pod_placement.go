package api

import (
	"net/netip"
	"strings"
)

// The existing restartability lookup also accepts names and DNS hints for
// diagnostics. Placement needs a stricter join so an old connection cannot be
// attributed to a replacement pod that happens to reuse its StatefulSet ordinal.
func observedGeneratorPodPlacement(entry GeneratorInventoryEntry, pods []KubePodRef, namespace string) *GeneratorPodPlacement {
	host, err := netip.ParseAddr(hostOfTarget(entry.Target))
	id := strings.TrimSpace(entry.GeneratorID)
	if err != nil || id == "" || namespace == "" {
		return nil
	}
	var found *GeneratorPodPlacement
	for _, pod := range pods {
		ip, err := netip.ParseAddr(pod.IP)
		if err != nil || ip.Unmap() != host.Unmap() || pod.Namespace != namespace {
			continue
		}
		// Duplicate IPs are ambiguous, even if only one claims our generator ID.
		if found != nil || pod.Name != id || pod.UID == "" || pod.NodeName == "" {
			return nil
		}
		found = &GeneratorPodPlacement{UID: pod.UID, Namespace: pod.Namespace, Name: pod.Name,
			NodeName: pod.NodeName, Ready: pod.Ready && pod.Phase == "Running", Terminating: pod.Terminating}
	}
	return found
}
