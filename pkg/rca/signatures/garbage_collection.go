package signatures

// GarbageCollection: collector pauses grew before the symptom.
var GarbageCollection = Signature{
	Name:          "garbage_collection",
	Required:      []string{KindGC},
	Indicators:    []Indicator{{KindGC, Increase}},
	Symptoms:      symptomsOfService,
	Competing:     []string{"resource_pressure"},
	Falsification: "garbage-collection time stays within its baseline before and during the symptom",
}
