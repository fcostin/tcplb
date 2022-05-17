package authn

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
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

// AuthenticatedTLSConn wraps a tls.Conn and exposes a GetClientID method
// that can be used to extract the canonical ClientID of the peer.
//
// Multiple goroutines may invoke methods on an AuthenticatedTLSConn
// simultaneously.
//
// Beware: despite the interface name, it is the responsibility of whoever
// established the tls.Conn to ensure peer authentication is required and
// occurs successfully during the TLS handshake.
type AuthenticatedTLSConn struct {
	*tls.Conn
}

// GetClientID attempts to extract the canonical ClientID representing
// the authenticated peer at the other side of an established TLS
// connection. See ExtractCanonicalClientID for details.
func (c *AuthenticatedTLSConn) GetClientID() (core.ClientID, error) {
	return ExtractCanonicalClientID(c.ConnectionState().VerifiedChains)
}

// InsecureTCPConn is not secure, and shouldn't be used outside of testing.
// It can be used to make a TCP Connection with a client appear as if it
// is an authenticated connection.
type InsecureTCPConn struct {
	*net.TCPConn
	ClientID core.ClientID
}

func (c *InsecureTCPConn) GetClientID() (core.ClientID, error) {
	return c.ClientID, nil
}
