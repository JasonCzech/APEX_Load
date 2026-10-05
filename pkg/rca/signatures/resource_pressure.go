package signatures

// ResourcePressure: the services or hosts behind the affected dependencies
// ran short of CPU or memory before the symptom.
var ResourcePressure = Signature{
	Name:          "resource_pressure",
	Required:      []string{KindCPU, KindMemory},
	Indicators:    []Indicator{{KindCPU, Increase}, {KindMemory, Increase}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"garbage_collection", "pool_exhaustion", "cold_start"},
	Falsification: "CPU and memory on the services and hosts behind the affected dependencies stay within their baselines through the window",
}
