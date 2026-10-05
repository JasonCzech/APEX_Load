package signatures

// Retries: clients or brokers retried failed work, adding load and delay.
var Retries = Signature{
	Name:          "retries",
	Required:      []string{KindRetries},
	Indicators:    []Indicator{{KindRetries, Increase}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"dead_letters", "network_phase"},
	Falsification: "retry counts stay at their baseline through the window",
}
