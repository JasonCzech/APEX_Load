package browser

import "github.com/jasonczech/apexload/pkg/protocol/browsercontract"

type DeviceProfile = browsercontract.DeviceProfile

func LookupDevice(name string) (DeviceProfile, bool) {
	return browsercontract.LookupDevice(name)
}
func ListDevices() []DeviceProfile {
	return browsercontract.ListDevices()
}
