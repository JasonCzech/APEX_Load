package engine

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func (vu *VUser) waitAtDistributedRendezvous(ctx context.Context, spec dsl.DistributedRendezvousSpec) error {
	if vu.DistributedRendezvous == nil || vu.ID < 0 {
		return fmt.Errorf("distributed rendezvous has no admitted group coordinator")
	}
	if vu.rendezvousRounds == nil {
		vu.rendezvousRounds = map[string]uint64{}
	}
	round := vu.rendezvousRounds[spec.Key]
	if round == math.MaxUint64 {
		return fmt.Errorf("distributed rendezvous round exhausted")
	}
	round++
	vu.rendezvousRounds[spec.Key] = round
	// The authoritative deadline belongs to the coordinator. This additional
	// local bound handles unavailable transport without inventing a release.
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutMS)*time.Millisecond+5*time.Second)
	defer cancel()
	out, err := vu.DistributedRendezvous.Wait(waitCtx, spec.Key, round, uint64(vu.ID))
	if err != nil {
		return fmt.Errorf("distributed rendezvous %q unavailable: %w", spec.Key, err)
	}
	if out.State == "timed_out" && spec.OnTimeout == "fail" {
		return fmt.Errorf("distributed rendezvous %q timed out: %d of %d participants arrived", spec.Key, out.Arrived, out.Target)
	}
	return nil
}
