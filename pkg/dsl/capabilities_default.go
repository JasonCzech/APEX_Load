//go:build !jms_solace_real

package dsl

func SolaceJMSRealAdapterAvailable() bool {
	return false
}
