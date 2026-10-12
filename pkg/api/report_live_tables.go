package api

import (
	"fmt"
	"strconv"
)

const liveTableReportTelemetryNote = "Live table operations are separate from target metrics and reset after controller adoption."
const liveTableReportLatencyNote = "Operation latency includes controller waits and commits; excludes transport."

func appendLiveTableReportPDF(lines *[]string, tables []RunArchiveLiveTableSummary) {
	if len(tables) == 0 {
		return
	}
	*lines = append(*lines, "Live data tables", "Live table operations are separate from target request metrics.", "Operation counters reset after controller adoption.", liveTableReportLatencyNote)
	for _, table := range tables {
		*lines = append(*lines,
			fmt.Sprintf("%s: %s, %s, %s; %s; revision %d", table.Alias, table.Profile, table.Durability, table.EndMode, table.State, table.Revision),
			fmt.Sprintf("  Seeded %d; pushed %d; available %d; leased %d", table.Counts.Seeded, table.Counts.Pushed, table.Counts.Available, table.Counts.Leased),
			fmt.Sprintf("  Acknowledged %d; popped %d; quarantined %d; discarded %d", table.Counts.Acknowledged, table.Counts.Popped, table.Counts.Quarantined, table.Counts.Discarded),
			fmt.Sprintf("  Delivery attempts %d; data bytes %d", table.Counts.DeliveryAttempts, table.DataBytes),
			fmt.Sprintf("  Operations %d; errors %d; unknown outcomes %d", table.OpCount, table.OpErrors, table.OpUnknown),
			fmt.Sprintf("  Operation latency: total %.3f ms; max %.3f ms", float64(table.OpLatencyTotalNS)/1e6, float64(table.OpLatencyMaxNS)/1e6),
		)
		if final := table.Finalization; final != nil {
			*lines = append(*lines, fmt.Sprintf("  Output: %s %s; pool %s; reason %s", final.Mode, final.State, reportProvidedValue(final.OutputPoolID), reportProvidedValue(final.Reason)))
		}
	}
}

func writeLiveTableReportCSVRows(write reportCSVWriteFunc, tables []RunArchiveLiveTableSummary) error {
	for _, table := range tables {
		rows := [][2]string{
			{"table_id", table.TableID}, {"profile", table.Profile}, {"durability", table.Durability}, {"end_mode", table.EndMode}, {"state", table.State},
			{"revision", strconv.FormatUint(table.Revision, 10)}, {"data_bytes", strconv.FormatInt(table.DataBytes, 10)},
			{"operation_telemetry_scope", liveTableReportTelemetryNote},
			{"operation_latency_basis", liveTableReportLatencyNote},
		}
		for _, counter := range []struct {
			name  string
			value uint64
		}{
			{"seeded", table.Counts.Seeded}, {"pushed", table.Counts.Pushed}, {"available", table.Counts.Available}, {"leased", table.Counts.Leased},
			{"acknowledged", table.Counts.Acknowledged}, {"popped", table.Counts.Popped}, {"quarantined", table.Counts.Quarantined}, {"discarded", table.Counts.Discarded}, {"delivery_attempts", table.Counts.DeliveryAttempts},
			{"op_count", table.OpCount}, {"op_errors", table.OpErrors}, {"op_unknown", table.OpUnknown}, {"op_latency_total_ns", table.OpLatencyTotalNS}, {"op_latency_max_ns", table.OpLatencyMaxNS},
		} {
			rows = append(rows, [2]string{counter.name, strconv.FormatUint(counter.value, 10)})
		}
		for i, upper := range []string{"1", "5", "10", "50", "100", "1000", "10000", "inf"} {
			rows = append(rows, [2]string{"op_latency_bucket_upper_ms_" + upper, strconv.FormatUint(table.OpLatencyBuckets[i], 10)})
		}
		if final := table.Finalization; final != nil {
			rows = append(rows, [2]string{"finalization_mode", final.Mode}, [2]string{"finalization_state", final.State}, [2]string{"output_pool_id", final.OutputPoolID}, [2]string{"finalization_reason", final.Reason})
		}
		for _, row := range rows {
			if err := write("live_table", table.Alias, row[0], row[1]); err != nil {
				return err
			}
		}
	}
	return nil
}
