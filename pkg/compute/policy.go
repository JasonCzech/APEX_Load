package compute

import "errors"

// CapacityPolicy is immutable once preparation begins. Empty selection preserves
// the existing pool lifecycle; explicit existing never mutates provider capacity.
type CapacityPolicy struct {
	Selection          string `json:"selection,omitempty"`
	Spot               string `json:"spot,omitempty"`
	OnDemandFloor      int    `json:"on_demand_floor,omitempty"`
	SpotCeilingPercent int    `json:"spot_ceiling_percent,omitempty"`
}

func (p CapacityPolicy) Validate() error {
	if p.Selection != "" && p.Selection != "existing" && p.Selection != "ephemeral" {
		return errors.New("capacity selection must be existing or ephemeral")
	}
	if p.Spot != "" && p.Spot != "off" && p.Spot != "preferred" && p.Spot != "required" {
		return errors.New("spot policy must be off, preferred, or required")
	}
	if p.OnDemandFloor < 0 || p.OnDemandFloor > 10000 || p.SpotCeilingPercent < 0 || p.SpotCeilingPercent > 100 {
		return errors.New("invalid on-demand floor or spot ceiling")
	}
	if p.Selection != "ephemeral" && (p.Spot != "" && p.Spot != "off" || p.OnDemandFloor != 0 || p.SpotCeilingPercent != 0) {
		return errors.New("spot allocation requires ephemeral selection")
	}
	if (p.Spot == "" || p.Spot == "off") && p.SpotCeilingPercent != 0 {
		return errors.New("spot ceiling requires an enabled spot policy")
	}
	return nil
}
func (p CapacityPolicy) SpotCount(desired int, info BackendInfo) (int, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if desired < 0 || desired > 10000 {
		return 0, errors.New("capacity outside policy bounds")
	}
	if p.Selection == "ephemeral" && !info.CanLaunch {
		return 0, ErrUnsupportedOperation
	}
	if p.OnDemandFloor > desired {
		return 0, errors.New("on-demand floor exceeds requested capacity")
	}
	if p.Spot == "" || p.Spot == "off" {
		return 0, nil
	}
	if !info.SupportsSpot {
		if p.Spot == "required" {
			return 0, errors.New("required spot policy is unsupported by this target")
		}
		return 0, nil
	}
	count := min(desired-p.OnDemandFloor, desired*p.SpotCeilingPercent/100)
	if count == 0 && p.Spot == "required" {
		return 0, errors.New("required spot policy cannot satisfy the floor and percentage ceiling")
	}
	return count, nil
}
