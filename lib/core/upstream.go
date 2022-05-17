package core

// Upstream represents an upstream that clients can be forwarded to.
type Upstream struct {
	Network string
	Address string
}

// UpstreamSet represents a set of Upstreams
type UpstreamSet map[Upstream]struct{}

// EmptyUpstreamSet returns a new UpstreamSet containing no Upstreams.
func EmptyUpstreamSet() UpstreamSet {
	return make(map[Upstream]struct{})
}

// NewUpstreamSet returns a new Upstream set containing the given Upstreams.
func NewUpstreamSet(upstreams ...Upstream) UpstreamSet {
	result := EmptyUpstreamSet()
	for _, u := range upstreams {
		result[u] = struct{}{}
	}
	return result
}

// Union returns a new UpstreamSet that is the union of the input UpstreamSets
func Union(lhs, rhs UpstreamSet) UpstreamSet {
	result := EmptyUpstreamSet()
	for k, _ := range lhs {
		result[k] = struct{}{}
	}
	for k, _ := range rhs {
		result[k] = struct{}{}
	}
	return result
}

// UnionUpdate updates the input acc UpstreamSet in-place by taking the union
// with the given rhs UpstreamSet. The modified input acc is returned.
func UnionUpdate(acc, rhs UpstreamSet) UpstreamSet {
	for k, _ := range rhs {
		acc[k] = struct{}{}
	}
	return acc
}

// TODO add UpstreamSet Intersection and IntersectionUpdate

// TODO add UpstreamSet Difference and DifferenceUpdate
