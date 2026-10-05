package signatures

// bonusLoadStep is what a symptom at a load step adds to a cold start.
const bonusLoadStep = 0.5

// ColdStart: instances were added, restarted or killed, and new instances
// served requests before they warmed up. A symptom that begins at a load
// step fits it better.
var ColdStart = Signature{
	Name:          "cold_start",
	Required:      []string{KindScaling},
	Indicators:    []Indicator{{KindScaling, ""}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"resource_pressure"},
	Falsification: "replica counts, restarts and kills stay constant through the window",
	Extra: func(ctx Context, r *Result) {
		if len(r.Supporting) > 0 && ctx.Symptom.LoadCoincident {
			r.Score += bonusLoadStep
			r.Notes = append(r.Notes, "the symptom began at a load step")
		}
	},
}
