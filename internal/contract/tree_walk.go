package contract

import "sort"

// TreeWalk reorders cis so that, within each story, every CI is preceded
// by its parent (when ParentInvocationID is set and the parent appears in
// the slice). Roots (no parent) come first sorted by Sequence; children
// follow their parent in Sequence order. Stable on Sequence ties.
//
// Story_d5d88a64: dynamic plan tree rendering. The portal timeline reads
// the result and indents children under their parent — a develop-loop CI
// renders directly below the develop CI whose close requested rework.
func TreeWalk(cis []ContractInstance) []ContractInstance {
	if len(cis) <= 1 {
		return append([]ContractInstance(nil), cis...)
	}
	idx := make(map[string]int, len(cis))
	for i, c := range cis {
		idx[c.ID] = i
	}
	children := make(map[string][]int, len(cis))
	roots := make([]int, 0, len(cis))
	for i, c := range cis {
		if c.ParentInvocationID == "" {
			roots = append(roots, i)
			continue
		}
		if _, ok := idx[c.ParentInvocationID]; !ok {
			// Parent isn't in this slice (e.g. cross-story). Treat as root
			// so we don't drop the row.
			roots = append(roots, i)
			continue
		}
		children[c.ParentInvocationID] = append(children[c.ParentInvocationID], i)
	}
	bySeq := func(a, b int) bool { return cis[a].Sequence < cis[b].Sequence }
	sort.SliceStable(roots, bySeq)
	for k := range children {
		c := children[k]
		sort.SliceStable(c, bySeq)
		children[k] = c
	}

	out := make([]ContractInstance, 0, len(cis))
	visited := make(map[string]struct{}, len(cis))
	var visit func(i int)
	visit = func(i int) {
		if _, dup := visited[cis[i].ID]; dup {
			return
		}
		visited[cis[i].ID] = struct{}{}
		out = append(out, cis[i])
		for _, ci := range children[cis[i].ID] {
			visit(ci)
		}
	}
	for _, r := range roots {
		visit(r)
	}
	// Catch any rows we somehow missed (cycles, self-parent) so the
	// caller never silently loses a row.
	for i := range cis {
		if _, ok := visited[cis[i].ID]; ok {
			continue
		}
		out = append(out, cis[i])
	}
	return out
}
