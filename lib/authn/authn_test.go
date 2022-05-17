package authn

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"github.com/stretchr/testify/require"
	"tcplb/lib/core"
	"testing"
)

func TestExtractCanonicalClientIDErrorsIfNilChains(t *testing.T) {
	_, err := ExtractCanonicalClientID(nil)
	require.ErrorIs(t, err, NoVerifiedChainError)
}

func TestExtractCanonicalClientIDErrorsIfZerothChainIsNil(t *testing.T) {
	chains := [][]*x509.Certificate{
		nil,
	}
	_, err := ExtractCanonicalClientID(chains)
	require.ErrorIs(t, err, NoVerifiedChainError)
}

func TestExtractCanonicalClientIDErrorsIfZerothChainIsEmpty(t *testing.T) {
	chains := [][]*x509.Certificate{
		{},
	}
	_, err := ExtractCanonicalClientID(chains)
	require.ErrorIs(t, err, NoVerifiedChainError)
}

func TestExtractCanonicalClientIDErrorsIfZerothCertInZerothChainHasBlankCommonName(t *testing.T) {
	leaf := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: "",
		},
	}
	chains := [][]*x509.Certificate{
		{leaf},
	}
	_, err := ExtractCanonicalClientID(chains)
	require.ErrorIs(t, err, InvalidClientIDError)
}

func TestExtractCanonicalClientIDCanSucceed(t *testing.T) {
	exampleID := "some test name"

	leaf := &x509.Certificate{
		Subject: pkix.Name{
			CommonName: exampleID,
		},
	}
	chains := [][]*x509.Certificate{
		{leaf},
	}
	clientId, err := ExtractCanonicalClientID(chains)

	expectedClientId := core.ClientID{
		Namespace: DefaultNamespace,
		Key:       exampleID,
	}

	require.NoError(t, err)
	require.Equal(t, expectedClientId, clientId)
}
