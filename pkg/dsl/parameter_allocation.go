package dsl

// AutoAllocatesUniqueRows reports whether a parameter's per-VUser block of
// rows is sized automatically for the run. auto_allocate says so explicitly. A
// unique file or list parameter with no block_size (and no
// value_for_each_vuser) is treated the same way, as VuGen's default
// "automatically allocate block size" is: without an allocation every VUser
// would walk the same rows, which unique selection must never do.
func AutoAllocatesUniqueRows(p Parameter) bool {
	if p.AutoAllocate {
		return true
	}
	if p.Selection != "unique" || p.BlockSize > 0 || p.ValueForEachVUser {
		return false
	}
	switch CanonicalParameterType(p.Type) {
	case "file", "list":
		return true
	}
	return false
}

// AllocatesUniqueRows reports whether a unique parameter reserves a block of
// rows (or numbers) for each VUser for the whole run, sized by block_size or
// automatically.
func AllocatesUniqueRows(p Parameter) bool {
	return p.Selection == "unique" && (p.BlockSize > 0 || AutoAllocatesUniqueRows(p))
}
