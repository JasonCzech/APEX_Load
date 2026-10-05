package signatures

// PoolExhaustion: a connection or thread pool filled, so requests waited for
// it.
var PoolExhaustion = Signature{
	Name:          "pool_exhaustion",
	Required:      []string{KindPool},
	Indicators:    []Indicator{{KindPool, Increase}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"database_wait", "resource_pressure"},
	Falsification: "pool usage and pool waits stay within their baselines while the symptom holds",
}
