package browser

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

func (b *Browser) dispatchSetGeolocation(action *protocol.Action, result *protocol.Result) (error, bool) {
	geo, err := actionGeolocation(action)
	if err != nil {
		return nil, failBrowserAction(result, err.Error())
	}
	if b.pwContext == nil {
		return nil, failBrowserAction(result, "browser context is not initialized")
	}
	if actionParamBool(action.Params, "grant_permission", true) {
		if err := b.pwContext.GrantPermissions([]string{"geolocation"}); err != nil {
			return err, false
		}
	}
	if err := b.pwContext.SetGeolocation(geo); err != nil {
		return err, false
	}
	b.geolocation = geo
	return nil, false
}

func actionGeolocation(action *protocol.Action) (*playwright.Geolocation, error) {
	latitude, hasLatitude := actionParamFloatValue(action.Params, "latitude")
	longitude, hasLongitude := actionParamFloatValue(action.Params, "longitude")
	if !hasLatitude {
		return nil, fmt.Errorf("browser_set_geolocation requires params.latitude")
	}
	if !hasLongitude {
		return nil, fmt.Errorf("browser_set_geolocation requires params.longitude")
	}
	geo := &playwright.Geolocation{Latitude: latitude, Longitude: longitude}
	if accuracy, ok := actionParamFloatValue(action.Params, "accuracy"); ok {
		geo.Accuracy = playwright.Float(accuracy)
	}
	if err := validatePlaywrightGeolocation(geo); err != nil {
		return nil, err
	}
	return geo, nil
}
