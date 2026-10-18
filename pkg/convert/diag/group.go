package diag

// GroupRepeated condenses identical informational/warning notes across source
// locations. Blockers remain separate, so every blocking location is actionable.
// The original diagnostics remain available to converter and fidelity checks.
func (d Diagnostics) GroupRepeated(maxExamples int) Diagnostics {
	if len(d) == 0 {
		return nil
	}
	if maxExamples < 1 {
		maxExamples = 1
	}
	type key struct {
		code, element, detail, source string
		severity                      Severity
	}
	indices := map[key]int{}
	locations := map[int]map[string]bool{}
	out := make(Diagnostics, 0, len(d))
	for _, item := range d {
		// Already grouped values and blockers retain their contract verbatim.
		if item.Severity == Blocker || item.Count > 1 {
			item.Locations = append([]string(nil), item.Locations...)
			out = append(out, item)
			continue
		}
		group := key{item.Code, item.Element, item.Detail, item.SourceRef, item.Severity}
		index, found := indices[group]
		if !found {
			index = len(out)
			indices[group] = index
			item.Count = 1
			item.Locations = nil
			out = append(out, item)
			locations[index] = map[string]bool{}
		} else {
			out[index].Count++
		}
		if item.Location != "" && !locations[index][item.Location] {
			if len(out[index].Locations) < maxExamples {
				locations[index][item.Location] = true
				out[index].Locations = append(out[index].Locations, item.Location)
			} else {
				out[index].LocationsTruncated = true
			}
		}
	}
	for i := range out {
		if out[i].Count == 1 {
			out[i].Count = 0
			out[i].Locations = nil
		}
	}
	return out
}
