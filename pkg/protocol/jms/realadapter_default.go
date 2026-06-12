//go:build !jms_solace_real

package jms

func newSolaceAdapter(cfg config) brokerAdapter {
	return unavailableSolaceAdapter{cfg: cfg}
}
