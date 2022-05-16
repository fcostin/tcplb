package core

// Upstream represents an upstream that clients can be forwarded to.
// Implementations must support comparison
// operators (==, !=) and have value semantics.
type Upstream interface {
	Name() string // name of the upstream (unique amongst all upstreams)
}

// USet represents a set of Upstreams
type USet map[Upstream]struct{}

// EmptyUSet returns a new Upstream set containing no Upstreams.
func EmptyUSet() USet {
	return make(map[Upstream]struct{})
}

// NewUSet returns a new Upstream set containing the given Upstreams.
func NewUSet(upstreams ...Upstream) USet {
	result := EmptyUSet()
	for _, u := range upstreams {
		result[u] = struct{}{}
	}
	return result
}

// Union returns a new Upstream set that is the union of the input Sets
func Union(lhs, rhs USet) USet {
	result := EmptyUSet()
	for k, _ := range lhs {
		result[k] = struct{}{}
	}
	for k, _ := range rhs {
		result[k] = struct{}{}
	}
	return result
}

// UnionUpdate updates the input acc USet in-place by taking the union
// with the given rhs USet. The modified input acc is returned.
func UnionUpdate(acc, rhs USet) USet {
	for k, _ := range rhs {
		acc[k] = struct{}{}
	}
	return acc
}

// TODO add USet Intersection and IntersectionUpdate

// TODO add USet Difference and DifferenceUpdate
