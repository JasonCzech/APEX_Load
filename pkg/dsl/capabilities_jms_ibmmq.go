package dsl

// IbmMqJMSRealAdapterAvailable reports whether this build can execute IBM MQ
// scripts against a real broker. IBM MQ currently has no linked MQI adapter in
// any build profile, so callers must fail closed unless the script uses mock
// mode.
func IbmMqJMSRealAdapterAvailable() bool {
	return false
}
