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

func TestNormalizeARTConvertsUnitsAndUsesPeakForSparseEvidence(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	workload, err := Normalize(Series{Name: "orders", Points: []Point{
		{Timestamp: start, Value: 10},
		{Timestamp: start.Add(15 * time.Second), Value: 20},
		{Timestamp: start.Add(30 * time.Second), Value: 30},
	}}, NormalizeOptions{Semantics: SemanticsTPS, SourceEnd: start.Add(45 * time.Second), TargetDuration: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	complete, err := NormalizeART(Series{Name: "art", Points: []Point{
		{Timestamp: start, Value: 0.1},
		{Timestamp: start.Add(15 * time.Second), Value: 0.2},
		{Timestamp: start.Add(30 * time.Second), Value: 0.4},
	}}, workload, "seconds")
	if err != nil {
		t.Fatal(err)
	}
	if complete.FallbackToPeak || complete.PeakARTMS != 400 || len(complete.Points) != 3 || complete.Points[1].ARTMS != 200 || complete.Points[1].OffsetMS != 5000 {
		t.Fatalf("complete ART = %#v", complete)
	}

	sparse, err := NormalizeART(Series{Name: "art", Points: []Point{
		{Timestamp: start, Value: 100},
		{Timestamp: start.Add(30 * time.Second), Value: 400},
	}}, workload, "milliseconds")
	if err != nil {
		t.Fatal(err)
	}
	if !sparse.FallbackToPeak || len(sparse.Points) != 1 || sparse.Points[0].ARTMS != 400 || len(sparse.Warnings) != 1 {
		t.Fatalf("sparse ART = %#v", sparse)
	}
	for _, test := range []struct {
		name   string
		series Series
		unit   string
	}{
		{name: "zero", series: Series{Points: []Point{{Timestamp: start, Value: 0}}}, unit: "milliseconds"},
		{name: "negative", series: Series{Points: []Point{{Timestamp: start, Value: -1}}}, unit: "milliseconds"},
		{name: "unknown unit", series: Series{Points: []Point{{Timestamp: start, Value: 1}}}, unit: "minutes"},
		{name: "outside window", series: Series{Points: []Point{{Timestamp: start.Add(-time.Second), Value: 1}}}, unit: "milliseconds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeART(test.series, workload, test.unit); err == nil {
				t.Fatal("expected ART validation error")
			}
		})
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
	book.SetCellValue("Sheet1", "C1", "art_seconds")
	book.SetCellValue("Sheet1", "C2", 0.2)
	book.SetCellValue("Sheet1", "C3", 0.4)
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
	artSeries, err := SeriesFromSheet(&sheets[0], TableOptions{TimestampColumn: "when", ValueColumns: []string{"art_seconds"}, Timezone: location})
	if err != nil || len(artSeries) != 1 || artSeries[0].Points[1].Value != 0.4 {
		t.Fatalf("XLSX ART series = %#v, %v", artSeries, err)
	}
}

func TestSeriesFromSheetRelativeTimestamps(t *testing.T) {
	sheet, err := ParseCSV([]byte("elapsed,orders\n0:00,10\n0:15,20\n25:00:30.5,30\n"))
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2000, time.January, 1, 12, 0, 0, 0, location)
	series, err := SeriesFromSheet(sheet, TableOptions{TimestampColumn: "elapsed", TimestampMode: "relative", RelativeStart: start, ValueColumns: []string{"orders"}, Timezone: location})
	if err != nil {
		t.Fatal(err)
	}
	if got := series[0].Points[0].Timestamp.Format(time.RFC3339); got != "2000-01-01T17:00:00Z" {
		t.Fatalf("relative zero timestamp = %s", got)
	}
	if got := series[0].Points[2].Timestamp.Sub(series[0].Points[0].Timestamp); got != 25*time.Hour+30*time.Second+500*time.Millisecond {
		t.Fatalf("relative span = %s", got)
	}
}

func TestParseRelativeTimestamp(t *testing.T) {
	for _, test := range []struct {
		value string
		want  time.Duration
		valid bool
	}{
		{value: "0:00", want: 0, valid: true},
		{value: "1:02:03.5", want: time.Hour + 2*time.Minute + 3500*time.Millisecond, valid: true},
		{value: "90m", want: 90 * time.Minute, valid: true},
		{value: "-0:01", valid: false},
		{value: "1:60", valid: false},
		{value: "1:02:60", valid: false},
		{value: "yesterday", valid: false},
	} {
		got, err := ParseRelativeTimestamp(test.value)
		if test.valid && (err != nil || got != test.want) {
			t.Errorf("ParseRelativeTimestamp(%q) = %s, %v; want %s", test.value, got, err, test.want)
		}
		if !test.valid && err == nil {
			t.Errorf("ParseRelativeTimestamp(%q) unexpectedly succeeded", test.value)
		}
	}
}

func TestNormalizeExplicitWindowWithRelativeOffsets(t *testing.T) {
	start := time.Date(2000, time.January, 1, 17, 0, 0, 0, time.UTC)
	series := Series{Points: []Point{
		{Timestamp: start, Value: 10},
		{Timestamp: start.Add(15 * time.Minute), Value: 20},
		{Timestamp: start.Add(30 * time.Minute), Value: 30},
		{Timestamp: start.Add(45 * time.Minute), Value: 40},
	}}
	got, err := Normalize(series, NormalizeOptions{
		Semantics: SemanticsTPS,
		GapPolicy: GapHold,
		Window: WindowSpec{
			Mode:            "explicit",
			RelativeOffsets: true,
			StartOffset:     15 * time.Minute,
			EndOffset:       45 * time.Minute,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Source.Points) != 2 || !got.Source.Points[0].Timestamp.Equal(start.Add(15*time.Minute)) {
		t.Fatalf("selected points = %#v", got.Source.Points)
	}
	if got.SourceDuration != 30*time.Minute {
		t.Fatalf("source duration = %s, want 30m", got.SourceDuration)
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
	if !got.SourceStart.Equal(start) || !got.SourceEnd.Equal(start.Add(15*time.Second)) {
		t.Fatalf("resolved source window = %s-%s", got.SourceStart, got.SourceEnd)
	}
	if got.SourceDuration != 15*time.Second || got.TargetDuration != 15*time.Second {
		t.Fatalf("durations = %s / %s", got.SourceDuration, got.TargetDuration)
	}
	if len(got.Points) != 2 || got.Points[0].Target != 10 || got.Points[1].Target != 20 {
		t.Fatalf("points = %#v", got.Points)
	}
}
