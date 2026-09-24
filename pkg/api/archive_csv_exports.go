package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Frozen at admission with the executable group snapshot, never resolved from
// today's mutable script library when a result is downloaded.
type RunArchiveCSVExport struct {
	Group  string        `json:"group"`
	Export dsl.CSVExport `json:"export"`
}
type RunArchiveCSVExportStatus struct {
	ID       string `json:"id"`
	Group    string `json:"group"`
	FileName string `json:"file_name"`
	Status   string `json:"status"`
	Rows     int64  `json:"rows"`
	Error    string `json:"error,omitempty"`
}

func declaredCSVExports(test *ManagedTest) []RunArchiveCSVExport {
	if test == nil {
		return nil
	}
	var out []RunArchiveCSVExport
	for _, g := range test.groupSpecs {
		if g.Script == nil {
			continue
		}
		for _, e := range g.Script.Config.Results.CSVExports {
			e.Columns = append([]dsl.CSVExportColumn(nil), e.Columns...)
			for i := range e.Columns {
				if e.Columns[i].DefaultValue != nil {
					value := csvExportDefaultValue(e.Columns[i])
					e.Columns[i].DefaultValue = &value
				}
			}
			out = append(out, RunArchiveCSVExport{Group: g.Name, Export: e})
		}
	}
	return out
}

func materializeDeclaredCSVExports(root string, manifest *RunArchiveManifest) {
	provenance := manifest.InitialProvenance
	if provenance == nil {
		provenance = &manifest.Provenance
	}
	names := map[string]int{}
	for _, declaration := range provenance.CSVExports {
		names[declaration.Export.FileName]++
	}
	for _, declaration := range provenance.CSVExports {
		e := declaration.Export
		status := RunArchiveCSVExportStatus{ID: e.ID, Group: declaration.Group, FileName: e.FileName, Status: "incomplete"}
		err := func() error {
			if manifest.CSVExportIncomplete != "" {
				return fmt.Errorf("%s", manifest.CSVExportIncomplete)
			}
			if manifest.CompletedAt.IsZero() {
				return fmt.Errorf("archive is not finalized")
			}
			// No ambiguous multi-group filename or source-specified filesystem write.
			if names[e.FileName] != 1 {
				return fmt.Errorf("duplicate CSV filename across groups; assign unique export filenames")
			}
			if name, err := normalizeArchiveArtifactName(e.FileName); err != nil || name != e.FileName || dsl.ValidateCSVExportFileName(name) != nil {
				return fmt.Errorf("invalid CSV filename")
			}
			if len(e.Columns) == 0 || len(e.Columns) > 32 {
				return fmt.Errorf("invalid CSV columns")
			}
			file, err := createArchiveArtifactExclusive(root, e.FileName, 0600, false)
			if err != nil {
				return err
			}
			rows, writeErr := writeDeclaredCSVExport(context.Background(), root, manifest, declaration, file)
			syncErr := file.Sync()
			closeErr := file.Close()
			if writeErr != nil || syncErr != nil || closeErr != nil {
				_ = os.Remove(filepath.Join(root, e.FileName))
				if writeErr != nil {
					return writeErr
				}
				if syncErr != nil {
					return syncErr
				}
				return closeErr
			}
			status.Rows = rows
			return nil
		}()
		if err != nil {
			status.Error = err.Error()
		} else {
			status.Status = "complete"
		}
		manifest.CSVExports = append(manifest.CSVExports, status)
	}
}

// Stream the complete, deterministically merged dataset. Budgets and malformed
// records are hard failures; a partially written file is never published.
func writeDeclaredCSVExport(ctx context.Context, root string, manifest *RunArchiveManifest, declaration RunArchiveCSVExport, destination io.Writer) (int64, error) {
	input, _, err := openArchiveArtifactForRead(root, manifest.TransactionsFile)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	writer := csv.NewWriter(&csvExportBudgetWriter{destination: destination, remaining: maxArchiveQueryBytes})
	header := make([]string, len(declaration.Export.Columns))
	for i, c := range declaration.Export.Columns {
		header[i] = c.Name
	}
	if err := writer.Write(header); err != nil {
		return 0, err
	}
	scanner := newArchiveJSONLScanner(input)
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	var count, rows int64
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return 0, err
		}
		var event TransactionEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return 0, err
		}
		count++
		if event.Group != declaration.Group || event.BusinessTransaction || event.Population == "transaction" {
			continue
		}
		matches := event.ActionName == scrub.ValueLabel(declaration.Export.ActionName, scrub.Policy{})
		if event.CSVExportIDs != nil {
			matches = slices.Contains(event.CSVExportIDs, declaration.Export.ID)
		}
		if !matches {
			continue
		}
		if event.ExtractionsIncomplete {
			return 0, fmt.Errorf("sample extraction retention was incomplete")
		}
		sanitizeArchivedTransaction(&event)
		row := make([]string, len(header))
		for i, c := range declaration.Export.Columns {
			switch {
			case c.Extraction != "":
				row[i] = event.Extractions[c.Extraction]
				if row[i] == "" && c.DefaultValue != nil {
					row[i] = csvExportDefaultValue(c)
				}
			case c.Metric == "latency_ms":
				if math.IsNaN(event.TTFBMs) || math.IsInf(event.TTFBMs, 0) || event.TTFBMs < 0 || event.TTFBMs >= math.MaxInt64 {
					return 0, fmt.Errorf("invalid first-byte latency")
				}
				row[i] = strconv.FormatInt(int64(event.TTFBMs), 10)
			default:
				return 0, fmt.Errorf("unsupported CSV column")
			}
		}
		if err := writer.Write(row); err != nil {
			return 0, err
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if count != manifest.TransactionCount {
		return 0, fmt.Errorf("archive incomplete: read %d records, expected %d", count, manifest.TransactionCount)
	}
	writer.Flush()
	return rows, writer.Error()
}

// CSV escaping and fallback columns can expand beyond the JSONL input size.
type csvExportBudgetWriter struct {
	destination io.Writer
	remaining   int64
}

func (w *csvExportBudgetWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("CSV export exceeds archive byte limit")
	}
	n, err := w.destination.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func csvExportDefaultValue(column dsl.CSVExportColumn) string {
	if column.DefaultValue == nil {
		return ""
	}
	if scrub.SensitiveName(column.Extraction) {
		return scrub.Redacted
	}
	return scrub.ValueLabel(*column.DefaultValue, scrub.Policy{})
}
