package signatures

// DatabaseWait: queries waited on locks, I/O or the database's own queues.
var DatabaseWait = Signature{
	Name:          "database_wait",
	Required:      []string{KindDBWait},
	Indicators:    []Indicator{{KindDBWait, Increase}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"pool_exhaustion", "resource_pressure"},
	Falsification: "database wait and lock times stay within their baselines while the symptom holds",
}
