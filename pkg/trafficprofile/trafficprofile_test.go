package trafficprofile

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestParseCSVAndNormalizeCounts(t *testing.T) {
	sheet, err := ParseCSV([]byte("timestamp,checkout\n2026-01-01T00:00:00Z,30\n2026-01-01T00:00:15Z,60\n2026-01-01T00:00:30Z,15\n"))
	if err != nil {
		t.Fatal(err)
	}
	series, err := SeriesFromSheet(sheet, TableOptions{TimestampColumn: "timestamp", ValueColumns: []string{"checkout"}, Timezone: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Normalize(series[0], NormalizeOptions{Semantics: SemanticsTransactionsPerBucket, SourceEnd: time.Date(2026, 1, 1, 0, 0, 45, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 3 || got.Points[0].Target != 2 || got.Points[1].Target != 4 || got.Points[2].Target != 1 {
		t.Fatalf("normalized points = %#v", got.Points)
	}
	if got.TargetDuration != 45*time.Second || got.DetectedBucket != 15*time.Second {
		t.Fatalf("durations = %s / %s", got.TargetDuration, got.DetectedBucket)
	}
}

func TestParseXLSXAndNaiveTimestampTimezone(t *testing.T) {
	book := excelize.NewFile()
	defer book.Close()
	book.SetCellValue("Sheet1", "A1", "when")
	book.SetCellValue("Sheet1", "B1", "orders")
	book.SetCellValue("Sheet1", "A2", time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC))
	book.SetCellValue("Sheet1", "B2", 15)
	book.SetCellValue("Sheet1", "A3", time.Date(2026, 1, 1, 8, 0, 15, 0, time.UTC))
	book.SetCellValue("Sheet1", "B3", 30)
	format := "yyyy-mm-dd hh:mm:ss"
	style, err := book.NewStyle(&excelize.Style{CustomNumFmt: &format})
	if err != nil {
		t.Fatal(err)
	}
	if err := book.SetCellStyle("Sheet1", "A2", "A3", style); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := book.Write(&buffer); err != nil {
		t.Fatal(err)
	}
	sheets, err := ParseXLSX(buffer.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SeriesFromSheet(&sheets[0], TableOptions{TimestampColumn: "when", ValueColumns: []string{"orders"}}); err == nil || !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("naive timestamp without timezone error = %v", err)
	}
	location, _ := time.LoadLocation("America/New_York")
	series, err := SeriesFromSheet(&sheets[0], TableOptions{TimestampColumn: "when", ValueColumns: []string{"orders"}, Timezone: location})
	if err != nil {
		t.Fatal(err)
	}
	if got := series[0].Points[0].Timestamp.Format(time.RFC3339); got != "2026-01-01T13:00:00Z" {
		t.Fatalf("UTC timestamp = %s", got)
	}
}

func TestNormalizeGapPolicies(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	series := Series{Points: []Point{{start, 1}, {start.Add(10 * time.Second), 2}, {start.Add(30 * time.Second), 3}, {start.Add(40 * time.Second), 4}}}
	for _, test := range []struct {
		policy GapPolicy
		count  int
		value  float64
	}{{GapHold, 5, 2}, {GapZero, 5, 0}, {GapDrop, 4, 3}} {
		got, err := Normalize(series, NormalizeOptions{Semantics: SemanticsTPS, GapPolicy: test.policy, SourceEnd: start.Add(50 * time.Second)})
		if err != nil {
			t.Fatalf("%s: %v", test.policy, err)
		}
		if len(got.Points) != test.count || got.Points[2].Target != test.value {
			t.Fatalf("%s points = %#v", test.policy, got.Points)
		}
	}
}

func TestNormalizeCounterGapDoesNotCreateFalseReset(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	series := Series{Points: []Point{{start, 100}, {start.Add(10 * time.Second), 130}, {start.Add(30 * time.Second), 160}, {start.Add(40 * time.Second), 170}}}
	got, err := Normalize(series, NormalizeOptions{Semantics: SemanticsCumulativeCounter, GapPolicy: GapZero, SourceEnd: start.Add(50 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 5 || got.Points[1].Target != 0 || got.Points[2].Target != 3 {
		t.Fatalf("counter gap points = %#v", got.Points)
	}
	if strings.Contains(strings.Join(got.Warnings, " "), "counter reset") {
		t.Fatalf("false reset warning: %v", got.Warnings)
	}
}

func TestNormalizeCounterResetAndCompressionLimit(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	series := Series{Name: "counter", Points: []Point{{start, 100}, {start.Add(15 * time.Second), 130}, {start.Add(30 * time.Second), 5}}}
	got, err := Normalize(series, NormalizeOptions{Semantics: SemanticsCumulativeCounter, SourceEnd: start.Add(45 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Points[0].Target != 2 || got.Points[1].Target != 5.0/15.0 || !strings.Contains(strings.Join(got.Warnings, " "), "counter reset") {
		t.Fatalf("counter result = %#v warnings=%v", got.Points, got.Warnings)
	}
	_, err = Normalize(series, NormalizeOptions{Semantics: SemanticsTPS, SourceEnd: start.Add(45 * time.Second), TargetDuration: 10 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "five") && !strings.Contains(err.Error(), "5s") {
		t.Fatalf("compression error = %v", err)
	}
}

func TestNormalizePreservesFullDayAtFifteenSeconds(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]Point, 5760)
	for i := range points {
		points[i] = Point{Timestamp: start.Add(time.Duration(i) * 15 * time.Second), Value: float64(i % 100)}
	}
	got, err := Normalize(Series{Name: "day", Points: points}, NormalizeOptions{Semantics: SemanticsTPS, SourceEnd: start.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 5760 || got.TargetDuration != 24*time.Hour {
		t.Fatalf("points=%d duration=%s", len(got.Points), got.TargetDuration)
	}
	if len(got.Warnings) > 2 || !strings.Contains(strings.Join(got.Warnings, " "), "5760 TPS intervals") {
		t.Fatalf("trackability warnings should be bounded summaries: %#v", got.Warnings)
	}
}

func TestNormalizeRejectsDuplicateAndNegative(t *testing.T) {
	now := time.Now().UTC()
	for _, points := range [][]Point{
		{{now, 1}, {now, 2}},
		{{now, 1}, {now.Add(time.Second), -1}},
	} {
		if _, err := Normalize(Series{Points: points}, NormalizeOptions{Semantics: SemanticsTPS}); err == nil {
			t.Fatalf("expected error for %#v", points)
		}
	}
}

func TestParseTimestampRejectsNonFiniteNumericValues(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf", "1e300", "-1e300", "9223372036854775807"} {
		if _, err := ParseTimestamp(value, time.UTC); err == nil {
			t.Fatalf("expected %q to fail", value)
		}
	}
}

func TestSelectPeakWindowUsesTransactionArea(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	series := Series{Points: []Point{
		{Timestamp: start, Value: 1},
		{Timestamp: start.Add(10 * time.Second), Value: 2},
		{Timestamp: start.Add(20 * time.Second), Value: 20},
		{Timestamp: start.Add(30 * time.Second), Value: 20},
		{Timestamp: start.Add(40 * time.Second), Value: 1},
	}}
	selected, err := selectWindow(series, WindowSpec{Mode: "peak", Duration: 21 * time.Second}, SemanticsTPS)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Points) != 3 || !selected.Points[0].Timestamp.Equal(start.Add(20*time.Second)) {
		t.Fatalf("selected points = %#v", selected.Points)
	}
}

func TestSelectPeakWindowHandlesMaximumSeriesSize(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]Point, MaxPointsPerSeries)
	for index := range points {
		points[index] = Point{Timestamp: start.Add(time.Duration(index) * 5 * time.Second), Value: float64(index % 17)}
	}
	selected, err := selectWindow(Series{Points: points}, WindowSpec{Mode: "peak", Duration: time.Hour}, SemanticsTPS)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Points) < 2 {
		t.Fatalf("selected %d points", len(selected.Points))
	}
}

func TestPeakWindowProratesPartialTransactionBucketsAndKeepsExactDuration(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	series := Series{Points: []Point{
		{Timestamp: start, Value: 100},
		{Timestamp: start.Add(10 * time.Second), Value: 100},
		{Timestamp: start.Add(20 * time.Second), Value: 1},
		{Timestamp: start.Add(30 * time.Second), Value: 1},
	}}
	got, err := Normalize(series, NormalizeOptions{Semantics: SemanticsTransactionsPerBucket, Window: WindowSpec{Mode: "peak", Duration: 15 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceDuration != 15*time.Second || got.TargetDuration != 15*time.Second {
		t.Fatalf("durations = %s / %s", got.SourceDuration, got.TargetDuration)
	}
	if len(got.Points) != 2 || got.Points[0].Target != 10 || got.Points[1].Target != 20 {
		t.Fatalf("points = %#v", got.Points)
	}
}
