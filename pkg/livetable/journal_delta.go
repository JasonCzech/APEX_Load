package livetable

import "maps"

// columnSplice records the already resolved structural change. Rotate moves
// exactly one existing first cell; it does not repeat selection or evaluate a
// vendor send flag during recovery. Extend represents absent padding compactly.
type columnSplice struct {
	Column string `json:"column"`
	Offset int    `json:"offset,omitempty"`
	Delete int    `json:"delete,omitempty"`
	Extend int    `json:"extend,omitempty"`
	Insert []Cell `json:"insert,omitempty"`
	Rotate bool   `json:"rotate,omitempty"`
}

type recordPatch struct {
	ControlClients []string `json:"control_clients,omitempty"`
	ID             string   `json:"id"`
	Version        uint64   `json:"version"`
	Lease          *Lease   `json:"lease,omitempty"`
	Attempts       uint32   `json:"attempts"`
	Quarantined    bool     `json:"quarantined,omitempty"`
}

func (t *Table) compactTransition(d transition) transition {
	if d.Close {
		// Terminal settlement is a deterministic structural operation over the
		// exact previous revision, not a list containing every stored record.
		if d.DiscardAll {
			d.DiscardAll = true
			d.Delete, d.Upsert, d.ColumnData = nil, nil, nil
		} else if t.s.Profile == ProfileNative {
			d.ReleaseOnClose = true
			d.Upsert = nil
		}
	}
	if len(d.ColumnSplices) > 0 {
		d.ColumnData = nil // materialized candidates are for validation/publication only
	}
	upserts := make([]record, 0, len(d.Upsert))
	for _, rec := range d.Upsert {
		if old, ok := t.s.Records[rec.Row.ID]; ok && maps.Equal(old.Row.Values, rec.Row.Values) {
			d.RecordPatches = append(d.RecordPatches, recordPatch{ControlClients: rec.ControlClients, ID: rec.Row.ID, Version: rec.Row.Version, Lease: rec.Lease, Attempts: rec.Attempts, Quarantined: rec.Quarantined})
		} else {
			upserts = append(upserts, rec)
		}
	}
	d.Upsert = upserts
	return d
}

func (s columnSplice) apply(cells []Cell) []Cell {
	if s.Rotate {
		out := append([]Cell(nil), cells[1:]...)
		return append(out, cells[0])
	}
	if s.Extend > len(cells) {
		cells = append(cells, make([]Cell, s.Extend-len(cells))...)
	}
	out := make([]Cell, 0, len(cells)-s.Delete+len(s.Insert))
	out = append(out, cells[:s.Offset]...)
	out = append(out, s.Insert...)
	return append(out, cells[s.Offset+s.Delete:]...)
}

func (t *Table) validateTransitionPatches(d transition) error {
	if d.RetireScope != "" {
		if !validOwnerScope(d.RetireScope) || d.Receipt != nil || len(d.Upsert) > 0 || len(d.RecordPatches) > 0 || len(d.Delete) > 0 || len(d.Cursors) > 0 || d.Close || d.Counts != nil || d.DataBytes != t.s.DataBytes || d.Fields != t.s.Fields {
			return fail(CodeInvalid, "invalid scope retirement transition")
		}
		for _, rec := range t.s.Records {
			if rec.Lease != nil && inOwnerScope(rec.Lease.ClientID, d.RetireScope) {
				return fail(CodeInvalid, "cannot retire an owned lease")
			}
		}
	}

	if (d.ReleaseOnClose || d.DiscardAll) && (!d.Close || (d.ReleaseOnClose && d.DiscardAll) || len(d.Upsert) != 0 || len(d.Delete) != 0 || len(d.RecordPatches) != 0) {
		return fail(CodeInvalid, "invalid terminal journal transition")
	}
	if d.ReleaseOnClose && t.s.Profile != ProfileNative {
		return fail(CodeInvalid, "invalid terminal lease settlement")
	}
	seen := map[string]bool{}
	for _, s := range d.ColumnSplices {
		if t.s.Profile != ProfileVTS || !contains(t.s.Columns, s.Column) || seen[s.Column] {
			return fail(CodeInvalid, "invalid journal column splice")
		}
		seen[s.Column] = true
		if _, duplicate := d.ColumnData[s.Column]; duplicate {
			return fail(CodeInvalid, "ambiguous journal column replacement")
		}
		n := len(t.s.Data[s.Column])
		if s.Extend < 0 || s.Extend > t.s.Limits.MaxRows || s.Offset < 0 || s.Delete < 0 || len(s.Insert) > t.s.Limits.MaxRows {
			return fail(CodeInvalid, "journal column splice exceeds bounds")
		}
		n = max(n, s.Extend)
		if s.Offset > n || s.Delete > n-s.Offset || n-s.Delete > t.s.Limits.MaxRows-len(s.Insert) || (s.Rotate && (n == 0 || s.Offset != 0 || s.Delete != 1 || len(s.Insert) != 0 || s.Extend != 0)) {
			return fail(CodeInvalid, "invalid journal column splice range")
		}
	}
	seen = map[string]bool{}
	for _, rec := range d.Upsert {
		seen[rec.Row.ID] = true
	}
	for _, id := range d.Delete {
		seen[id] = true
	}
	for _, p := range d.RecordPatches {
		if _, exists := t.s.Records[p.ID]; t.s.Profile != ProfileNative || !exists || seen[p.ID] {
			return fail(CodeInvalid, "invalid journal record patch")
		}
		seen[p.ID] = true
	}
	return nil
}
