// Package convertresult holds the parts of a conversion result every importer
// can express, independent of the format it converted.
//
// It exists because each converter package declares its own ConvertResult and
// its own ConvertStats. Those stats are genuinely format-specific — thread
// groups, channels, port types, and panels are different things and should not
// be forced into one shape. What every format can answer is how much of the
// source made it across, and until now that mapping lived only in the
// cross-importer contract test.
package convertresult

// Accounting is the completeness of one conversion, in the terms every format
// shares. It lets an operator compare a Postman import against a JMeter one
// without knowing what either counts internally.
//
// Detected is what the source offered. Converted, Skipped, and ReviewOnly
// partition it: actions that replay, source the converter deliberately did not
// carry across, and actions preserved for review rather than execution.
type Accounting struct {
	Detected   int `json:"detected"`
	Converted  int `json:"converted"`
	Skipped    int `json:"skipped"`
	ReviewOnly int `json:"review_only"`
}

// New builds an Accounting from the counts a converter tracks. convertedTotal is
// the converter's own converted-action count, which includes review-only
// actions; they are separated out here so the three buckets partition Detected.
func New(detected, convertedTotal, skipped, reviewOnly int) Accounting {
	converted := convertedTotal - reviewOnly
	if converted < 0 {
		converted = 0
	}
	if skipped < 0 {
		skipped = 0
	}
	return Accounting{
		Detected:   detected,
		Converted:  converted,
		Skipped:    skipped,
		ReviewOnly: reviewOnly,
	}
}

// Empty reports whether nothing was detected, which is how a converter that
// cannot account for its source stays out of the response.
func (a Accounting) Empty() bool {
	return a == Accounting{}
}
