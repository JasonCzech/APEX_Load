package signatures

// Rebalance: a consumer-group rebalance paused consumption.
var Rebalance = Signature{
	Name:          "rebalance",
	Required:      []string{KindRebalance},
	Indicators:    []Indicator{{KindRebalance, Increase}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"consumer_lag"},
	Falsification: "no consumer-group rebalance occurs in the window",
}
