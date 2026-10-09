package api

import "testing"

// TestPDFASCIIText verifies the ASCII-only PDF writer folds common non-ASCII
// report content (accented Latin letters, non-decomposable letters, and
// punctuation) to sensible ASCII instead of rendering '?'.
func TestPDFASCIIText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Página de inicio", "Pagina de inicio"},
		{"Camión / Añadir", "Camion / Anadir"},
		{"Café Straße", "Cafe Strasse"},
		{"Ørsted Æther œuvre", "Orsted AEther oeuvre"},
		{"verdict — passed", "verdict - passed"},
		{"“quoted” ‘value’", "\"quoted\" 'value'"},
		{"plain ascii", "plain ascii"},
		{"dropped ≥ 4 (breached ÷ classified) ≤ 5% × 2", "dropped >= 4 (breached / classified) <= 5% x 2"},
	}
	for _, tc := range cases {
		if got := pdfASCIIText(tc.in); got != tc.want {
			t.Errorf("pdfASCIIText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// A truly unmappable non-Latin rune still degrades to '?', not a crash.
	if got := pdfASCIIText("emoji \U0001F600 x"); got != "emoji ? x" {
		t.Errorf("pdfASCIIText(emoji) = %q, want %q", got, "emoji ? x")
	}
}
