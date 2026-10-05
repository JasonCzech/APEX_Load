package signatures

// DeadLetters: messages that could not be processed were dead-lettered.
var DeadLetters = Signature{
	Name:          "dead_letters",
	Required:      []string{KindDeadLetters},
	Indicators:    []Indicator{{KindDeadLetters, Increase}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"retries", "consumer_lag"},
	Falsification: "dead-letter counts stay at their baseline through the window",
}
