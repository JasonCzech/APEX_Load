//go:build !jms_servicebus_real

package jms

func newServiceBusAdapter(cfg config) brokerAdapter {
	return unavailableServiceBusAdapter{cfg: cfg}
}
