package browser

import (
	"fmt"
	"net/url"
	"strings"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browsercontract"
)

// A browser recording of a Salesforce journey usually starts by signing in:
// the login page, perhaps identity verification, then the app. Those steps
// belong in init, so each virtual user signs in once rather than every
// iteration, and they are tagged metadata.salesforce_auth for the session
// strategies that replace a recorded login. A one-time verification code is
// masked like a password: it cannot replay.

// salesforceOneTimeCode reports a fill of a one-time verification code.
func salesforceOneTimeCode(step Step) bool {
	if step.Kind != StepFill {
		return false
	}
	if step.Element != nil && strings.EqualFold(strings.TrimSpace(step.Element.Attributes["autocomplete"]), "one-time-code") {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(step.URL))
	return err == nil && strings.HasPrefix(parsed.Path, "/_ui/identity/verification/")
}

// salesforceSignInSteps is the converted steps' leading run of steps on
// Salesforce login and identity-verification pages, by step sequence. It is
// empty when the steps converted are only a sign-in, or when a step there was
// placed in another section.
func salesforceSignInSteps(steps []Step, stepRange StepRange) map[int]bool {
	signIn := map[int]bool{}
	for _, step := range steps {
		if step.skipForConvert() || !stepRange.contains(step.Seq) {
			continue
		}
		if !browserprotocol.SalesforceSignInURL(step.URL) {
			if len(signIn) == 0 {
				return nil
			}
			return signIn
		}
		if normalizeSection(step.Section) != SectionActions {
			return nil
		}
		signIn[step.Seq] = true
	}
	// Nothing came after the sign-in: it is the journey.
	return nil
}

// moveSalesforceSignInToInit puts the timeline up to the sign-in's last step
// in init. It refuses, saying why, when a transaction started there ends
// after it: init and the iteration cannot share one.
//
// Only a sign-in the timeline holds as one leading run moves. The timeline is
// in time order and the sign-in run in step order, and the two can differ: a
// click inside a frame of the login page can be stamped before a hover
// reported ahead of it. A step that is not part of the sign-in never moves,
// and a sign-in it splits stays where it was recorded, whole, with a warning:
// half of it in init would leave the iteration clicking a login page it no
// longer shows.
func moveSalesforceSignInToInit(entries []timelineEntry, signIn map[int]bool) (int, string) {
	last := -1
	for i, entry := range entries {
		if entry.step == nil {
			continue
		}
		if !signIn[entry.step.Seq] {
			break
		}
		last = i
	}
	for _, entry := range entries[last+1:] {
		if entry.step != nil && signIn[entry.step.Seq] {
			return 0, "The recorded Salesforce sign-in runs in every iteration: another recorded step happened in the middle of it, and init cannot hold part of the sign-in. Exclude that step and convert again to sign in once per virtual user."
		}
	}
	if last < 0 {
		return 0, ""
	}
	open := map[string]int{}
	for _, entry := range entries[:last+1] {
		if entry.marker == nil {
			continue
		}
		switch entry.marker.Kind {
		case MarkerTransactionStart:
			open[entry.marker.Name]++
		case MarkerTransactionEnd:
			if open[entry.marker.Name] > 0 {
				open[entry.marker.Name]--
			}
		}
	}
	// A transaction marked around the sign-in ends after its last step: the
	// end markers that follow, before anything else, belong to it.
	for next := last + 1; next < len(entries) && openTransactions(open); next++ {
		marker := entries[next].marker
		if marker == nil || marker.Kind != MarkerTransactionEnd || open[marker.Name] == 0 {
			break
		}
		open[marker.Name]--
		last = next
	}
	for name, count := range open {
		if count > 0 {
			return 0, fmt.Sprintf("The recorded Salesforce sign-in runs in every iteration: transaction %q starts during it and ends after it, and init cannot hold part of a transaction.", name)
		}
	}
	moved := 0
	for i := range entries[:last+1] {
		if normalizeSection(entries[i].section) != SectionActions {
			continue
		}
		entries[i].section = SectionInit
		if entries[i].step != nil {
			moved++
		}
	}
	return moved, ""
}

func openTransactions(open map[string]int) bool {
	for _, count := range open {
		if count > 0 {
			return true
		}
	}
	return false
}
