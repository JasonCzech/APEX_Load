package duckdb

import "github.com/jasonczech/apexload/pkg/analysis"

// Drop optional proof for the entire bounded population when its escaped JSON
// aggregate is excessive. Unknown summaries retain scanning; counts and time
// extrema remain exact. No file, identity membership set, or row is truncated.
// The budget is the controller's manifest, whose partitions carry full object
// keys rather than these outputs' short handles.
func workerBoundPartitionMetadata(outputs []workerOutput, project, dataset string, mappingVersion int) error {
	for _, out := range outputs {
		if out.Partition != nil && out.Partition.Validate() != nil {
			return errWorkerProtocol
		}
	}
	fits := func() bool {
		if _, err := analysis.MarshalBoundedAnalysisJSON(outputs, analysis.MaxPartitionManifestBytes); err != nil {
			return false
		}
		stats := make([]analysis.NormalizedPartitionStats, 0, len(outputs))
		for _, out := range outputs {
			if out.Partition != nil {
				stats = append(stats, *out.Partition)
			}
		}
		return analysis.PartitionStatsFitManifest(project, dataset, mappingVersion, stats)
	}
	if fits() {
		return nil
	}
	for i := range outputs {
		if outputs[i].Partition != nil {
			outputs[i].Partition.Identity = nil
		}
	}
	if fits() {
		return nil
	}
	for i := range outputs {
		if outputs[i].Partition != nil {
			outputs[i].Partition.Metric = nil
		}
	}
	if !fits() {
		return errWorkerProtocol
	}
	return nil
}
