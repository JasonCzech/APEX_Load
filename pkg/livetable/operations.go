package livetable

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

func isRead(op Operation) bool  { return op == OpRead || op == OpReadColumn }
func canWait(op Operation) bool { return op == OpPop || op == OpLease || op == OpNext || op == OpRead }

func (t *Table) validateRequest(r Request) error {
	switch r.Operation {
	case OpRead, OpReadColumn, OpNext, OpPush, OpPop, OpLease, OpRenew, OpAck, OpRelease, OpUpdate, OpIncrement, OpClear, OpRotate, OpCreateColumn:
	default:
		return fail(CodeInvalid, "unknown table operation")
	}
	if !isRead(r.Operation) && (!validName(r.ClientID) || r.Sequence == 0) {
		return fail(CodeInvalid, "mutations require a bounded client identity and positive sequence")
	}
	if len(r.OwnerID) > 256 || len(r.RecordID) > 256 || len(r.LeaseToken) > 256 || len(r.Cursor) > 256 {
		return fail(CodeInvalid, "operation identifier too long")
	}
	if r.Wait < 0 || r.Wait > t.s.Limits.MaxWait || r.Wait > 0 && !canWait(r.Operation) {
		return fail(CodeInvalid, "invalid wait policy")
	}
	if r.Position < 0 || r.Position > t.s.Limits.MaxRows || r.Limit < 0 || r.Limit > t.s.Limits.MaxRows {
		return fail(CodeInvalid, "row range exceeds supported bounds")
	}
	if r.LeaseDuration < 0 || r.LeaseDuration > t.s.Limits.MaxLeaseDuration {
		return fail(CodeInvalid, "lease duration exceeds supported bounds")
	}
	if r.Operation != OpCreateColumn {
		if err := t.validateValues(r.Values); err != nil {
			return err
		}
		if err := t.validateValues(r.Expected); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, c := range r.Columns {
		if !validName(c) || seen[c] {
			return fail(CodeInvalid, "column selectors must be unique and bounded")
		}
		seen[c] = true
		if r.Operation != OpCreateColumn && !contains(t.s.Columns, c) {
			return fail(CodeInvalid, "unknown column")
		}
	}
	if len(r.Columns) > t.s.Limits.MaxColumns {
		return fail(CodeCapacity, "column limit exceeded")
	}
	if len(r.Values) > 0 && r.Operation != OpPush && r.Operation != OpUpdate {
		return fail(CodeInvalid, "values are only valid for push and update")
	}
	if (len(r.Expected) > 0 || r.ExpectedVersion != nil) && r.Operation != OpUpdate && r.Operation != OpIncrement && r.Operation != OpClear {
		return fail(CodeInvalid, "conditions are only valid for update, increment, and clear")
	}
	if r.Limit > 0 && r.Operation != OpRead && r.Operation != OpReadColumn {
		return fail(CodeInvalid, "limit is only valid for read operations")
	}
	if r.Cursor != "" && r.Operation != OpNext {
		return fail(CodeInvalid, "cursor is only valid for next")
	}
	if r.LeaseDuration > 0 && r.Operation != OpLease && r.Operation != OpRenew {
		return fail(CodeInvalid, "lease duration is only valid for lease and renew")
	}
	if r.LeaseToken != "" && r.Operation != OpAck && r.Operation != OpRelease && r.Operation != OpRenew {
		return fail(CodeInvalid, "lease token is only valid for acquisition mutations")
	}
	if t.s.Profile == ProfileVTS && (r.Operation == OpLease || r.Operation == OpRenew || r.Operation == OpAck || r.Operation == OpRelease) {
		return fail(CodeInvalid, "VTS columns do not support leases")
	}
	if t.s.Profile == ProfileNative && (r.Operation == OpRotate || r.Operation == OpCreateColumn || r.Operation == OpReadColumn) {
		return fail(CodeInvalid, "operation requires the VTS columns profile")
	}
	return nil
}

func (t *Table) evaluate(r Request, now time.Time) (Result, transition, error) {
	d := transition{FormatVersion: formatVersion, DataBytes: t.s.DataBytes, Fields: t.s.Fields}
	var result Result
	var err error
	if t.s.Profile == ProfileNative {
		result, err = t.native(r, now, &d)
	} else {
		result, err = t.vts(r, &d)
	}
	if err != nil {
		return Result{}, d, err
	}
	if !isRead(r.Operation) {
		d.DataBytes, d.Fields = t.measureData(&d)
		if d.DataBytes > t.s.Limits.MaxBytes || d.Fields > t.s.Limits.MaxFields {
			return Result{}, d, fail(CodeCapacity, "table data budget exceeded")
		}
	}
	result.Revision = t.s.Revision
	return result, d, nil
}

func selected(r Request, all []string) []string {
	if len(r.Columns) > 0 {
		return r.Columns
	}
	return all
}
func filterRow(row Row, columns []string) Row {
	result := Row{ID: row.ID, Version: row.Version, Values: map[string]string{}}
	for _, col := range columns {
		if value, ok := row.Values[col]; ok {
			result.Values[col] = value
		}
	}
	return result
}
func matches(row Row, r Request) bool {
	if r.ExpectedVersion != nil && row.Version != *r.ExpectedVersion {
		return false
	}
	for k, value := range r.Expected {
		if actual, ok := row.Values[k]; !ok || actual != value {
			return false
		}
	}
	return true
}
func owned(r Request, l *Lease) bool {
	owner := r.OwnerID
	if owner == "" {
		owner = r.ClientID
	}
	return l != nil && l.Token == r.LeaseToken && l.ClientID == owner
}

func (t *Table) native(r Request, now time.Time, d *transition) (Result, error) {
	columns := selected(r, t.s.Columns)
	counts := t.s.Counts
	if r.Operation == OpPush {
		if r.RecordID != "" {
			return Result{}, fail(CodeInvalid, "new record identities are assigned by the table")
		}
		if len(r.Values) == 0 {
			return Result{}, fail(CodeInvalid, "push requires values")
		}
		if len(t.s.Records) >= t.s.Limits.MaxRows {
			return Result{}, fail(CodeCapacity, "record limit exceeded")
		}
		id := "record:" + newToken()
		if _, exists := t.s.Records[id]; exists {
			return Result{}, fail(CodeConflict, "record identity already exists")
		}
		row := Row{ID: id, Version: 1, Values: copyValues(r.Values)}
		d.Upsert = []record{{Row: row}}
		d.Append = []string{id}
		counts.Pushed++
		counts.Available++
		d.Counts = &counts
		return Result{Status: StatusOK, Row: &row}, nil
	}
	if r.Operation == OpAck || r.Operation == OpRelease || r.Operation == OpRenew {
		if r.RecordID == "" || r.LeaseToken == "" {
			return Result{}, fail(CodeInvalid, "lease operations require record identity and acquisition token")
		}
		rec, exists := t.s.Records[r.RecordID]
		if !exists || !owned(r, rec.Lease) || !rec.Lease.ExpiresAt.After(now) {
			return Result{}, fail(CodeLease, "lease is absent, expired, or owned by another acquisition")
		}
		rec = copyRecord(rec)
		switch r.Operation {
		case OpAck:
			d.Delete = []string{rec.Row.ID}
			counts.Leased--
			counts.Acknowledged++
		case OpRelease:
			rec.Lease = nil
			rec.Row.Version++
			counts.Leased--
			if rec.Attempts >= t.s.Limits.MaxDeliveryAttempts {
				rec.Quarantined = true
				counts.Quarantined++
			} else {
				counts.Available++
			}
			d.Upsert = []record{rec}
		case OpRenew:
			ttl := r.LeaseDuration
			if ttl == 0 {
				ttl = t.s.Limits.DefaultLeaseDuration
			}
			until := now.Add(ttl)
			if until.After(rec.Lease.MaxExpiresAt) {
				until = rec.Lease.MaxExpiresAt
			}
			if !until.After(now) {
				return Result{}, fail(CodeLease, "lease maximum lifetime reached")
			}
			if until.After(rec.Lease.ExpiresAt) {
				rec.Lease.ExpiresAt = until
			}
			rec.Row.Version++
			d.Upsert = []record{rec}
		}
		d.Counts = &counts
		row := filterRow(rec.Row, columns)
		return Result{Status: StatusOK, Row: &row, Lease: rec.Lease}, nil
	}
	var rec record
	var exists bool
	if r.RecordID != "" {
		rec, exists = t.s.Records[r.RecordID]
	} else {
		pos := r.Position
		if pos == 0 {
			pos = 1
		}
		if r.Operation == OpNext && t.s.Counts.Available > 0 {
			cursor := r.Cursor
			if cursor == "" {
				cursor = "sequential"
			}
			if _, ok := t.s.Cursors[cursor]; !ok && len(t.s.Cursors) >= t.s.Limits.MaxClients {
				return Result{}, fail(CodeCapacity, "cursor budget exhausted")
			}
			offset := t.s.Cursors[cursor]
			if offset == math.MaxUint64 {
				return Result{}, fail(CodeCapacity, "cursor sequence exhausted")
			}
			pos = int(offset%t.s.Counts.Available) + 1
			d.Cursors = map[string]uint64{cursor: offset + 1}
		}
		available := 0
		scanned := 0
		for _, id := range t.s.Order {
			scanned++
			if scanned > t.s.Limits.MaxScanCells {
				return Result{}, fail(CodeCapacity, "queue scan exceeds supported cost")
			}
			v, ok := t.s.Records[id]
			if ok && v.Lease == nil && !v.Quarantined {
				available++
				if available == pos {
					rec = v
					exists = true
					break
				}
			}
		}
	}
	if !exists {
		return Result{Status: StatusEmpty}, nil
	}
	if (r.Operation == OpPop || r.Operation == OpLease || r.Operation == OpNext) && (rec.Lease != nil || rec.Quarantined) {
		return Result{Status: StatusEmpty}, nil
	}
	if r.Operation != OpRead && rec.Lease != nil {
		return Result{}, fail(CodeLease, "leased records cannot be mutated outside their acquisition")
	}
	if !matches(rec.Row, r) {
		return Result{Status: StatusConditionFailed}, nil
	}
	row := filterRow(rec.Row, columns)
	switch r.Operation {
	case OpRead, OpNext:
		return Result{Status: StatusOK, Row: &row}, nil
	case OpPop:
		d.Delete = []string{rec.Row.ID}
		counts.Available--
		counts.Popped++
		d.Counts = &counts
	case OpLease:
		if rec.Attempts >= t.s.Limits.MaxDeliveryAttempts {
			return Result{}, fail(CodeLease, "record delivery limit exhausted")
		}
		rec = copyRecord(rec)
		rec.Attempts++
		rec.Row.Version++
		owner := r.OwnerID
		if owner == "" {
			owner = r.ClientID
		}
		ttl := r.LeaseDuration
		if ttl == 0 {
			ttl = t.s.Limits.DefaultLeaseDuration
		}
		rec.Lease = &Lease{Token: newToken(), RecordID: rec.Row.ID, ClientID: owner, ExpiresAt: now.Add(ttl), MaxExpiresAt: now.Add(t.s.Limits.MaxLeaseLifetime), DeliveryAttempt: rec.Attempts}
		d.Upsert = []record{rec}
		counts.Available--
		counts.Leased++
		counts.DeliveryAttempts++
		d.Counts = &counts
		row = filterRow(rec.Row, columns)
		return Result{Status: StatusOK, Row: &row, Lease: rec.Lease}, nil
	case OpUpdate, OpClear, OpIncrement:
		rec = copyRecord(rec)
		if r.Operation == OpUpdate {
			if len(r.Values) == 0 {
				return Result{}, fail(CodeInvalid, "update requires values")
			}
			for k, v := range r.Values {
				rec.Row.Values[k] = v
			}
		}
		if r.Operation == OpClear {
			for _, col := range columns {
				if _, present := rec.Row.Values[col]; present {
					rec.Row.Values[col] = ""
				}
			}
		}
		if r.Operation == OpIncrement {
			if len(columns) != 1 {
				return Result{}, fail(CodeInvalid, "increment requires exactly one column")
			}
			value, present := rec.Row.Values[columns[0]]
			next, err := increment(value, present, r.Delta)
			if err != nil {
				return Result{}, err
			}
			rec.Row.Values[columns[0]] = next
		}
		rec.Row.Version++
		d.Upsert = []record{rec}
		row = filterRow(rec.Row, columns)
	default:
		return Result{}, fail(CodeInvalid, "unsupported native operation")
	}
	return Result{Status: StatusOK, Row: &row}, nil
}

func increment(value string, present bool, delta int64) (string, error) {
	if !present {
		return "", fail(CodeInvalid, "increment requires a present integer")
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return "", fail(CodeInvalid, "increment requires a base-10 signed 64-bit integer")
	}
	if delta > 0 && n > math.MaxInt64-delta || delta < 0 && n < math.MinInt64-delta {
		return "", fail(CodeInvalid, "integer overflow")
	}
	return strconv.FormatInt(n+delta, 10), nil
}

func (t *Table) vts(r Request, d *transition) (Result, error) {
	columns := selected(r, t.s.Columns)
	pos := r.Position
	if pos == 0 {
		pos = 1
	}
	if r.RecordID != "" || r.LeaseToken != "" || r.ExpectedVersion != nil {
		return Result{}, fail(CodeInvalid, "VTS operations use positions and expected cell values")
	}
	if r.Operation == OpCreateColumn {
		if len(r.Columns) != 1 || len(r.Values) > 0 {
			return Result{}, fail(CodeInvalid, "create_column requires one new column")
		}
		if contains(t.s.Columns, r.Columns[0]) {
			return Result{Status: StatusConditionFailed}, nil
		}
		if len(t.s.Columns) >= t.s.Limits.MaxColumns {
			return Result{}, fail(CodeCapacity, "column limit exceeded")
		}
		d.Columns = append(append([]string(nil), t.s.Columns...), r.Columns[0])
		d.ColumnData = map[string][]Cell{r.Columns[0]: {}}
		return Result{Status: StatusOK, Columns: d.Columns}, nil
	}
	if r.Operation == OpReadColumn {
		if len(columns) != 1 {
			return Result{}, fail(CodeInvalid, "read_column requires one column")
		}
		cells := t.s.Data[columns[0]]
		if pos > len(cells) {
			return Result{Status: StatusEmpty}, nil
		}
		limit := r.Limit
		if limit == 0 {
			limit = 1024
		}
		end := pos - 1 + limit
		if end > len(cells) {
			end = len(cells)
		}
		return Result{Status: StatusOK, Cells: append([]Cell(nil), cells[pos-1:end]...), Columns: columns, Count: int64(end - pos + 1)}, nil
	}
	rowCount := 0
	for _, col := range columns {
		if len(t.s.Data[col]) > rowCount {
			rowCount = len(t.s.Data[col])
		}
	}
	if r.Operation == OpNext {
		if rowCount == 0 {
			return Result{Status: StatusEmpty}, nil
		}
		cursor := r.Cursor
		if cursor == "" {
			cursor = "sequential"
		}
		if _, ok := t.s.Cursors[cursor]; !ok && len(t.s.Cursors) >= t.s.Limits.MaxClients {
			return Result{}, fail(CodeCapacity, "cursor budget exhausted")
		}
		offset := t.s.Cursors[cursor]
		pos = int(offset%uint64(rowCount)) + 1
		d.Cursors = map[string]uint64{cursor: offset + 1}
	}
	if r.Operation == OpPop || r.Operation == OpRotate {
		pos = 1
	}
	row := t.vtsRow(pos, columns)
	if r.Operation != OpPush && r.Operation != OpUpdate && pos > rowCount {
		return Result{Status: StatusEmpty}, nil
	}
	if !matches(row, r) {
		return Result{Status: StatusConditionFailed}, nil
	}
	if r.Operation == OpRead || r.Operation == OpNext {
		if r.Limit > 1 && r.Operation == OpRead {
			limit := r.Limit
			if pos-1+limit > rowCount {
				limit = rowCount - pos + 1
			}
			if limit > t.s.Limits.MaxScanCells/len(columns) {
				return Result{}, fail(CodeCapacity, "range exceeds supported scan cost")
			}
			rows := make([]Row, 0, limit)
			for i := 0; i < limit; i++ {
				rows = append(rows, t.vtsRow(pos+i, columns))
			}
			return Result{Status: StatusOK, Rows: rows, Count: int64(len(rows))}, nil
		}
		return Result{Status: StatusOK, Row: &row}, nil
	}
	d.ColumnData = map[string][]Cell{}
	scan := 0
	for _, col := range columns {
		scan += len(t.s.Data[col])
		if scan > t.s.Limits.MaxScanCells {
			return Result{}, fail(CodeCapacity, "column mutation exceeds supported scan cost")
		}
	}
	switch r.Operation {
	case OpPush:
		if len(r.Values) == 0 {
			return Result{}, fail(CodeInvalid, "push requires values")
		}
		// Independently append supplied columns. Missing keys do not append an
		// empty cell; this is the column model, not a native row queue.
		row.Values = copyValues(r.Values)
		for col, value := range r.Values {
			cells := t.s.Data[col]
			if len(cells) >= t.s.Limits.MaxRows {
				return Result{}, fail(CodeCapacity, "column row limit exceeded")
			}
			copyCells := append([]Cell(nil), cells...)
			d.ColumnData[col] = append(copyCells, Cell{Present: true, Value: value})
			d.ColumnSplices = append(d.ColumnSplices, columnSplice{Column: col, Offset: len(cells), Insert: []Cell{{Present: true, Value: value}}})
		}
	case OpPop, OpRotate:
		for _, col := range columns {
			cells := t.s.Data[col]
			if len(cells) == 0 {
				continue
			}
			copyCells := append([]Cell(nil), cells[1:]...)
			if r.Operation == OpRotate {
				copyCells = append(copyCells, cells[0])
			}
			d.ColumnData[col] = copyCells
			splice := columnSplice{Column: col, Delete: 1}
			if r.Operation == OpRotate {
				splice.Rotate = true
			}
			d.ColumnSplices = append(d.ColumnSplices, splice)
		}
	case OpUpdate, OpClear, OpIncrement:
		if r.Operation == OpUpdate && len(r.Values) == 0 {
			return Result{}, fail(CodeInvalid, "update requires values")
		}
		if r.Operation == OpIncrement && len(columns) != 1 {
			return Result{}, fail(CodeInvalid, "increment requires one column")
		}
		updates := map[string]string{}
		if r.Operation == OpUpdate {
			updates = r.Values
		}
		if r.Operation == OpClear {
			for _, col := range columns {
				cells := t.s.Data[col]
				if pos <= len(cells) && cells[pos-1].Present {
					updates[col] = ""
				}
			}
		}
		if r.Operation == OpIncrement {
			value, present := row.Values[columns[0]]
			next, err := increment(value, present, r.Delta)
			if err != nil {
				return Result{}, err
			}
			updates[columns[0]] = next
		}
		for col, value := range updates {
			cells := append([]Cell(nil), t.s.Data[col]...)
			if pos > len(cells) {
				cells = append(cells, make([]Cell, pos-len(cells))...)
			}
			cells[pos-1] = Cell{Present: true, Value: value}
			d.ColumnData[col] = cells
			d.ColumnSplices = append(d.ColumnSplices, columnSplice{Column: col, Offset: pos - 1, Delete: 1, Extend: max(pos, len(t.s.Data[col])), Insert: []Cell{{Present: true, Value: value}}})
			row.Values[col] = value
		}
	default:
		return Result{}, fail(CodeInvalid, fmt.Sprintf("operation %q is unsupported for VTS columns", r.Operation))
	}
	return Result{Status: StatusOK, Row: &row}, nil
}

func (t *Table) vtsRow(pos int, columns []string) Row {
	row := Row{Version: t.s.Revision, Values: map[string]string{}}
	for _, col := range columns {
		cells := t.s.Data[col]
		if pos > 0 && pos <= len(cells) && cells[pos-1].Present {
			row.Values[col] = cells[pos-1].Value
		}
	}
	return row
}

func recordCost(r record) (int64, int) {
	n := int64(192 + encodedStringBytes(r.Row.ID))
	for k, v := range r.Row.Values {
		n += int64(64 + encodedStringBytes(k) + encodedStringBytes(v))
	}
	if r.Lease != nil {
		n += int64(192 + encodedStringBytes(r.Lease.ClientID) + encodedStringBytes(r.Lease.Token) + encodedStringBytes(r.Lease.RecordID))
	}
	return n, len(r.Row.Values)
}
func columnCost(c []Cell) (int64, int) {
	n := int64(len(c)) * 32
	for _, cell := range c {
		n += int64(encodedStringBytes(cell.Value))
	}
	return n, len(c)
}

func encodedStringBytes(s string) int { encoded, _ := json.Marshal(s); return len(encoded) }

func (t *Table) measureData(d *transition) (int64, int) {
	if d == nil {
		n := int64(0)
		fields := 0
		for _, col := range t.s.Columns {
			n += int64(64 + len(col))
		}
		if t.s.Profile == ProfileNative {
			for _, r := range t.s.Records {
				cost, f := recordCost(r)
				n += cost
				fields += f
			}
		} else {
			for _, cells := range t.s.Data {
				cost, f := columnCost(cells)
				n += cost
				fields += f
			}
		}
		return n, fields
	}
	n, fields := t.s.DataBytes, t.s.Fields
	for _, id := range d.Delete {
		if old, ok := t.s.Records[id]; ok {
			cost, f := recordCost(old)
			n -= cost
			fields -= f
		}
	}
	for _, r := range d.Upsert {
		if old, ok := t.s.Records[r.Row.ID]; ok {
			cost, f := recordCost(old)
			n -= cost
			fields -= f
		}
		cost, f := recordCost(r)
		n += cost
		fields += f
	}
	splices := make(map[string]columnSplice, len(d.ColumnSplices))
	for _, splice := range d.ColumnSplices {
		splices[splice.Column] = splice
	}
	for col, cells := range d.ColumnData {
		if splice, ok := splices[col]; ok {
			if splice.Rotate {
				continue
			} // exact conservation; no need to rescan values
			old := t.s.Data[col]
			padding := max(0, splice.Extend-len(old))
			n += int64(padding) * int64(32+encodedStringBytes(""))
			fields += padding
			start := min(splice.Offset, len(old))
			end := min(splice.Offset+splice.Delete, len(old))
			removed, count := columnCost(old[start:end])
			// Deletions may target an absent cell created by this extension.
			absent := splice.Delete - count
			n -= removed + int64(absent)*int64(32+encodedStringBytes(""))
			fields -= splice.Delete
			added, count := columnCost(splice.Insert)
			n += added
			fields += count
			continue
		}
		old, f := columnCost(t.s.Data[col])
		n -= old
		fields -= f
		cost, f := columnCost(cells)
		n += cost
		fields += f
	}
	for _, col := range d.Columns {
		if !contains(t.s.Columns, col) {
			n += int64(64 + len(col))
		}
	}
	return n, fields
}
