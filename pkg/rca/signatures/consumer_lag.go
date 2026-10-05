package signatures

// ConsumerLag: consumers fell behind the messages produced to them.
var ConsumerLag = Signature{
	Name:          "consumer_lag",
	Required:      []string{KindConsumerLag},
	Indicators:    []Indicator{{KindConsumerLag, Increase}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"rebalance", "dead_letters"},
	Falsification: "consumer lag stays within its baseline while the symptom holds",
}
