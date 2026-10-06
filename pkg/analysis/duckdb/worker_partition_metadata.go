package duckdb

import "github.com/jasonczech/apexload/pkg/analysis"

// Drop optional proof for the entire bounded population when its escaped JSON
// aggregate is excessive. Unknown summaries retain scanning; counts and time
// extrema remain exact. No file, identity membership set, or row is truncated.
func workerBoundPartitionMetadata(outputs []workerOutput) error {
	for _, out := range outputs {
		if out.Partition != nil && out.Partition.Validate() != nil {
			return errWorkerProtocol
		}
	}
	fits := func() bool {
		_, err := analysis.MarshalBoundedAnalysisJSON(outputs, analysis.MaxPartitionManifestBytes)
		return err == nil
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
