package grafana

import "strings"

// unitLabels maps a Grafana unit id to the display label APEX renders. APEX uses the
// unit purely as a free-text axis label — there is no numeric scaling anywhere in the
// chart or report path — so the mapping is presentational only. Ids absent here fall
// through to unitMapping's switch (for the families that need a diagnostic) or to an
// honest "no APEX equivalent" info.
var unitLabels = map[string]string{
	// Dimensionless.
	"none":   "",
	"short":  "",
	"":       "",
	"locale": "",
	"sci":    "",

	// Percentages.
	"percent": "%",

	// Durations.
	"s":  "s",
	"ms": "ms",
	"µs": "µs",
	"us": "µs",
	"ns": "ns",
	"m":  "min",
	"h":  "h",
	"d":  "d",

	// Data (IEC vs SI distinction preserved in the label).
	"bytes":     "bytes",
	"decbytes":  "B",
	"bits":      "bits",
	"decbits":   "bit",
	"kbytes":    "KiB",
	"mbytes":    "MiB",
	"gbytes":    "GiB",
	"tbytes":    "TiB",
	"pbytes":    "PiB",
	"deckbytes": "kB",
	"decmbytes": "MB",
	"decgbytes": "GB",

	// Throughput.
	"Bps":    "B/s",
	"binBps": "B/s",
	"bps":    "bit/s",
	"binbps": "bit/s",
	"KBs":    "KiB/s",
	"MBs":    "MiB/s",
	"GBs":    "GiB/s",
	"KiBs":   "KiB/s",
	"MiBs":   "MiB/s",

	// Rates.
	"pps":    "pkt/s",
	"ops":    "ops/s",
	"reqps":  "req/s",
	"rps":    "req/s",
	"wps":    "writes/s",
	"iops":   "IO/s",
	"cps":    "counts/s",
	"hertz":  "Hz",
	"rowsps": "rows/s",
	"eps":    "events/s",

	// Temperature.
	"celsius":    "°C",
	"fahrenheit": "°F",
	"kelvin":     "K",

	// Currency.
	"currencyUSD": "USD",
	"currencyEUR": "EUR",
	"currencyGBP": "GBP",
	"currencyJPY": "JPY",
	"currencyCNY": "CNY",
}

// unitDisposition tells the converter how to treat a Grafana unit beyond its label:
// whether it recognized the id, and whether a diagnostic should accompany it because
// Grafana applies a transform (e.g. percentunit ×100) or formatting APEX cannot mirror.
type unitDisposition struct {
	Label      string
	Recognized bool
	// Note, when non-empty, is surfaced as a diag.Info so the discrepancy is explicit
	// rather than a silent difference in what the user sees vs. what Grafana showed.
	Note string
}

// mapUnit resolves a Grafana unit id to an APEX display label and an honest
// disposition. It never silently applies a numeric transform: percentunit values are
// fractions Grafana multiplies by 100 for display, and APEX renders them raw, so the
// caller is told rather than the data being quietly rescaled.
func mapUnit(grafanaUnit string) unitDisposition {
	id := strings.TrimSpace(grafanaUnit)

	// Families that carry a diagnostic.
	switch id {
	case "percentunit":
		return unitDisposition{
			Label:      "%",
			Recognized: true,
			Note:       "Grafana unit \"percentunit\" holds fractions (0–1) and multiplies by 100 for display; APEX renders the raw value without rescaling.",
		}
	case "dtdurations", "dthms", "clocks":
		return unitDisposition{Label: "s", Recognized: true, Note: "Grafana composite duration formatting is not reproduced; APEX renders seconds."}
	case "dtdurationms", "clockms":
		return unitDisposition{Label: "ms", Recognized: true, Note: "Grafana composite duration formatting is not reproduced; APEX renders milliseconds."}
	case "hex", "hex0x", "bool", "bool_yes_no", "bool_on_off":
		return unitDisposition{Label: "", Recognized: true, Note: "Grafana formatting-only unit \"" + id + "\" is ignored; APEX renders the raw numeric value."}
	}

	if label, ok := unitLabels[id]; ok {
		return unitDisposition{Label: label, Recognized: true}
	}
	return unitDisposition{Label: "", Recognized: false}
}
