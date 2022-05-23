package authn

import (
	"crypto/x509"
	"errors"
	"tcplb/lib/core"
)

const (
	DefaultNamespace = "CommonName"
)

var NoVerifiedChainError = errors.New("authentication failure - no verified chain")
var InvalidClientIDError = errors.New("authentication failure - invalid client id")

// ExtractCanonicalClientID attempts to extract a canonical ClientID from the given
// verifiedChains, which are assumed to be arranged as per crypto/tls documentation.
//
// The CommonName attribute of the leaf certificate Subject of the 0-th chain is used
// to determine the canonical ClientID.
//
// In the following circumstances, extraction fails, and a NoVerifiedChainError error
// is returned:
// - zero chains are given
// - the 0th chain does not contain a certificate in position 0.
//
// In the following circumstances, extraction fails, and a InvalidClientIDError error
// is returned:
// - the 0th certificate in the 0th chain has an empty-string value for Subject CommonName
func ExtractCanonicalClientID(verifiedChains [][]*x509.Certificate) (core.ClientID, error) {
	if len(verifiedChains) == 0 {
		return core.ClientID{}, NoVerifiedChainError
	}
	if len(verifiedChains[0]) == 0 {
		return core.ClientID{}, NoVerifiedChainError
	}
	leafPeerCert := verifiedChains[0][0]
	if leafPeerCert == nil {
		return core.ClientID{}, NoVerifiedChainError
	}
	key := leafPeerCert.Subject.CommonName
	if key == "" {
		return core.ClientID{}, InvalidClientIDError
	}
	clientID := core.ClientID{
		Namespace: DefaultNamespace,
		Key:       key,
	}
	return clientID, nil
}
