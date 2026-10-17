package browser

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browsercontract"
)

func TestBrowserContractDefaultsAndDevicesMatchAuthoring(t *testing.T) {
	var runtime Browser
	runtime.applyDefaultConfig()
	authored := dsl.DefaultScriptConfig().Browser
	if runtime.engine != authored.Engine || runtime.headless != *authored.Headless || runtime.orientation != authored.Orientation || runtime.viewportW != authored.Viewport.Width || runtime.viewportH != authored.Viewport.Height || runtime.cacheEnabled != *authored.CacheEnabled || runtime.cacheContentURLs != *authored.CacheContentURLs || runtime.downloadResources != authored.DownloadResources {
		t.Fatalf("browser defaults do not match authoring: %+v", authored)
	}
	if got, want := ListDevices(), browsercontract.ListDevices(); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime devices = %v, want %v", got, want)
	}
	for _, profile := range ListDevices() {
		if got, ok := LookupDevice(profile.Name); !ok || got != profile {
			t.Fatalf("device %s differs", profile.Name)
		}
	}
	if _, err := protocol.Get("browser"); err != nil {
		t.Fatalf("browser registration lost: %v", err)
	}
}
