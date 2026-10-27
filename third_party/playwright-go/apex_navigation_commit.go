package playwright

// APEXNavigationCommit is a snapshot of the native frame navigation event.
// Request is present only for an observed successful new-document commit that
// names its native request. Same-document and failed navigations still advance
// Sequence and clear Request. A snapshot is not a DOM/lifecycle join by itself.
type APEXNavigationCommit struct {
	Sequence     uint64
	Request      Request
	NewDocument  bool
	SameDocument bool
	Failed       bool
}

// APEXFrameNavigationCommit returns only observed native events; initializers
// and URLs never acquire an inferred navigation identity. Callers joining a DOM
// observation must verify an unchanged sequence and exact request on both sides.
func APEXFrameNavigationCommit(frame Frame) APEXNavigationCommit {
	f, ok := frame.(*frameImpl)
	if !ok || f == nil {
		return APEXNavigationCommit{}
	}
	f.RLock()
	defer f.RUnlock()
	return APEXNavigationCommit{
		Sequence:     f.navigationSequence,
		Request:      f.navigationRequest,
		NewDocument:  f.navigationNewDocument,
		SameDocument: f.navigationSameDocument,
		Failed:       f.navigationFailed,
	}
}
