package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/browserresource"
	"reflect"
)

func DecodeBrowserResourcePage(raw []byte) (*BrowserResourcePage, error) {
	return browserresource.DecodeBrowserResourcePage(raw)
}
func browserResourcePageWireValue(d *json.Decoder, typ reflect.Type, nullable bool, depth int, remaining *int) bool {
	return browserresource.WireValue(d, typ, nullable, depth, remaining)
}
