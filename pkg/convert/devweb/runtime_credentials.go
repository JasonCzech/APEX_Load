package devweb

import (
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// projectDevWebRuntimeCredentials preserves authored rts.yml HTTP proxy values
// verbatim in the executable script without synthesizing {{secret.DEVWEB_*}}
// placeholders or credential-based execution gates.
func projectDevWebRuntimeCredentials(
	_ *dsl.Script,
	_ string,
	_ *credentialpolicy.NameRegistry,
) ([]credentialpolicy.Requirement, []importreview.Finding, error) {
	return nil, nil, nil
}
