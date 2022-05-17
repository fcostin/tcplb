package core

// ClientID represents the identity of an authenticated client.
type ClientID struct {
	Namespace string // Namespace is the namespace for the type of identifier
	Key       string // Key is the canonical identifier for this client
}

// TODO consider adding a Hash method. C.f. UniformlyBoundedClientReserver
