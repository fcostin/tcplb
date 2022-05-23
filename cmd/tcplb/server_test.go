package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"os"
	"path/filepath"
	"tcplb/lib/authn"
	"tcplb/lib/core"
	"tcplb/lib/slog"
	"testing"
)

/* This is a heavyweight suite of tests that tests the entire server.
 * These tests require that the TCPLB_TESTBED_ROOT is set to the testbed
 * directory, containing various example server and client certificates.
 *
 * To set this up, then run this test suite:
 * 1. ensure working dir is root directory of the repo checkout
 * 2. `make allkeys`
 * 3. `export TCPLB_TESTBED_ROOT=$(pwd)/testbed`
 * 4. `go test -v ./cmd/...`
 */

const (
	/* demo application protocol:
	 * demo client sends ApplicationClientHello, awaits ApplicationServerHello
	 * demo server awaits ApplicationClientHello, sends ApplicationServerHello
	 */
	ApplicationClientHello = `HEY THERE I AM CLIENT\n`
	ApplicationServerHello = `OH HEY THERE I AM SERVER\n`
)

func getTestbedRoot(t *testing.T) string {
	root, ok := os.LookupEnv("TCPLB_TESTBED_ROOT")
	if !ok {
		t.Fatalf("environment variable TCPLB_TESTBED_ROOT must be defined")
	}
	return root
}

func testResource(t *testing.T, relativePath string) string {
	root := getTestbedRoot(t)
	return filepath.Join(root, relativePath)
}

func newTestServerConfig(certFile, keyFile, rootCAPath string, upstreams []core.Upstream, authorizedClient core.ClientID) *Config {
	return &Config{
		ListenNetwork:           defaultListenNetwork,
		ListenAddress:           "0.0.0.0:0", // bind to an arbitrary free port
		ApplicationIdleTimeout:  defaultApplicationIdleTimeout,
		MaxConnectionsPerClient: 5,
		Upstreams:               upstreams,
		TLS: &TLSConfig{
			ServerCertFile: certFile,
			ServerKeyFile:  keyFile,
			RootCAPath:     rootCAPath,
		},
		Authentication: &AuthnConfig{AllowAnonymous: false},
		Authorization: &AuthzConfig{
			AuthorizedClients: []core.ClientID{authorizedClient},
		},
	}
}

type demoAppServer struct {
	Listener   net.Listener
	HandleFunc func(conn net.Conn)
}

func newDemoAppServer(network, address string) (*demoAppServer, error) {
	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return &demoAppServer{
		Listener:   listener,
		HandleFunc: demoHandleFunc,
	}, nil
}

func (s *demoAppServer) Serve() error {
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
		}
		go s.HandleFunc(conn)
	}
}

func demoHandleFunc(conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	buffer := make([]byte, len(ApplicationClientHello))
	_, err := io.ReadFull(conn, buffer)
	if err != nil {
		return
	}
	_, _ = conn.Write([]byte(ApplicationServerHello))
	return
}

func (s *demoAppServer) Close() error {
	return s.Listener.Close()
}

func makeDemoAppRequestTLS(ctx context.Context, certFile, keyFile, rootCAPath, serverAddress, serverName string) error {
	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != err {
		return err
	}
	rootCAs, err := loadRootCAs(rootCAPath)
	if err != nil {
		return err
	}
	certificates := []tls.Certificate{
		clientCert,
	}
	tlsConfig := &tls.Config{
		Certificates: certificates,
		ClientCAs:    x509.NewCertPool(), // we plan to accept no client TLS connections
		RootCAs:      rootCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		ServerName:   serverName,
	}

	d := &net.Dialer{}
	network := "tcp"
	tlsConn, err := tls.DialWithDialer(d, network, serverAddress, tlsConfig)
	if err != nil {
		return err
	}
	defer func() { _ = tlsConn.Close() }()
	written, err := tlsConn.Write([]byte(ApplicationClientHello))
	if err != nil {
		return err
	}
	if written != len(ApplicationClientHello) {
		return errors.New("failed to send ApplicationClientHello")
	}
	buffer := make([]byte, len(ApplicationServerHello))
	_, err = io.ReadFull(tlsConn, buffer)
	if err != nil {
		return err
	}

	if bytes.Equal(buffer[:len(ApplicationServerHello)], []byte(ApplicationServerHello)) {
		return nil
	}
	return errors.New("did not receive expected ApplicationServerHello")
}

func makeDemoAppRequestTCP(ctx context.Context, serverAddress string) error {
	d := &net.Dialer{}
	network := "tcp"
	tcpConn, err := d.Dial(network, serverAddress)
	if err != nil {
		return err
	}
	defer func() { _ = tcpConn.Close() }()
	written, err := tcpConn.Write([]byte(ApplicationClientHello))
	if err != nil {
		return err
	}
	if written != len(ApplicationClientHello) {
		return errors.New("failed to send ApplicationClientHello")
	}
	buffer := make([]byte, len(ApplicationServerHello))
	_, err = io.ReadFull(tcpConn, buffer)
	if err != nil {
		return err
	}

	if bytes.Equal(buffer[:len(ApplicationServerHello)], []byte(ApplicationServerHello)) {
		return nil
	}
	return errors.New("did not receive expected ApplicationServerHello")
}

func TestServerAcceptsTrustedTLSClient(t *testing.T) {
	logger := slog.GetDefaultLogger()

	serverName := "tcplb-server-strong"
	serverCertFile := testResource(t, "tcplb-server-strong/cert.pem")
	serverKeyFile := testResource(t, "tcplb-server-strong/key.pem")

	clientName := "client-strong"
	clientId := core.ClientID{Namespace: authn.DefaultNamespace, Key: clientName}
	clientCertFile := testResource(t, "client-strong/cert.pem")
	clientKeyFile := testResource(t, "client-strong/key.pem")

	// launch demo app server to act as the upstream
	upstreamServer, err := newDemoAppServer("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		srvErr := upstreamServer.Serve()
		logger.Error(&slog.LogRecord{Msg: "server.Serve returned error", Error: srvErr})
	}()
	defer func() {
		_ = upstreamServer.Close()
	}()

	// Configure tcplb server to:
	// - forward to the upstream
	// - trust the client
	theUpstream := core.Upstream{Network: "tcp", Address: upstreamServer.Listener.Addr().String()}
	upstreams := []core.Upstream{
		theUpstream,
	}
	config := newTestServerConfig(serverCertFile, serverKeyFile, clientCertFile, upstreams, clientId)

	server, err := NewServer(logger, config)
	require.NoError(t, err, err)

	serverAddress := server.Listener.Addr().String()

	// start the upstream server
	go func() {
		srvErr := server.Serve()
		if srvErr != nil {
			logger.Error(&slog.LogRecord{Msg: "server.Serve returned error", Error: srvErr})
		}
	}()

	ctx := context.Background()
	clientErr := makeDemoAppRequestTLS(ctx, clientCertFile, clientKeyFile, serverCertFile, serverAddress, serverName)

	require.NoError(t, clientErr)

	defer func() {
		closeErr := server.Close()
		require.NoError(t, closeErr)
	}()
}

func TestServerRejectsTCPClient(t *testing.T) {
	logger := slog.GetDefaultLogger()

	serverCertFile := testResource(t, "tcplb-server-strong/cert.pem")
	serverKeyFile := testResource(t, "tcplb-server-strong/key.pem")

	clientName := "client-strong"
	clientId := core.ClientID{Namespace: authn.DefaultNamespace, Key: clientName}
	clientCertFile := testResource(t, "client-strong/cert.pem")

	// launch demo app server to act as the upstream
	upstreamServer, err := newDemoAppServer("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		srvErr := upstreamServer.Serve()
		logger.Error(&slog.LogRecord{Msg: "server.Serve returned error", Error: srvErr})
	}()
	defer func() {
		_ = upstreamServer.Close()
	}()

	// Configure tcplb server to:
	// - forward to the upstream
	// - trust the client
	theUpstream := core.Upstream{Network: "tcp", Address: upstreamServer.Listener.Addr().String()}
	upstreams := []core.Upstream{
		theUpstream,
	}
	config := newTestServerConfig(serverCertFile, serverKeyFile, clientCertFile, upstreams, clientId)

	server, err := NewServer(logger, config)
	require.NoError(t, err, err)

	serverAddress := server.Listener.Addr().String()

	// start the upstream server
	go func() {
		srvErr := server.Serve()
		if srvErr != nil {
			logger.Error(&slog.LogRecord{Msg: "server.Serve returned error", Error: srvErr})
		}
	}()

	ctx := context.Background()
	clientErr := makeDemoAppRequestTCP(ctx, serverAddress)
	// From client perspective, when the tcplb server gets a message that doesn't
	// resemble a TLS ClientHello, the server unceremoniously closes the connection.
	require.ErrorIs(t, clientErr, io.EOF)

	defer func() {
		closeErr := server.Close()
		require.NoError(t, closeErr)
	}()
}

func TestServerRejectsUntrustedTLSClient(t *testing.T) {
	logger := slog.GetDefaultLogger()

	serverName := "tcplb-server-strong"
	serverCertFile := testResource(t, "tcplb-server-strong/cert.pem")
	serverKeyFile := testResource(t, "tcplb-server-strong/key.pem")

	trustedClientName := "client-strong"
	trustedClientCertFile := testResource(t, "client-strong/cert.pem")
	trustedClientId := core.ClientID{Namespace: authn.DefaultNamespace, Key: trustedClientName}

	clientCertFile := testResource(t, "client-unknown/cert.pem")
	clientKeyFile := testResource(t, "client-unknown/key.pem")

	// launch demo app server to act as the upstream
	upstreamServer, err := newDemoAppServer("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		srvErr := upstreamServer.Serve()
		logger.Error(&slog.LogRecord{Msg: "server.Serve returned error", Error: srvErr})
	}()
	defer func() {
		_ = upstreamServer.Close()
	}()

	// Configure tcplb server to:
	// - forward to the upstream
	// - trust the trusted client only (not the unknown one)
	theUpstream := core.Upstream{Network: "tcp", Address: upstreamServer.Listener.Addr().String()}
	upstreams := []core.Upstream{
		theUpstream,
	}
	config := newTestServerConfig(serverCertFile, serverKeyFile, trustedClientCertFile, upstreams, trustedClientId)

	server, err := NewServer(logger, config)
	require.NoError(t, err, err)

	serverAddress := server.Listener.Addr().String()

	// start the upstream server
	go func() {
		srvErr := server.Serve()
		if srvErr != nil {
			logger.Error(&slog.LogRecord{Msg: "server.Serve returned error", Error: srvErr})
		}
	}()

	ctx := context.Background()
	clientErr := makeDemoAppRequestTLS(ctx, clientCertFile, clientKeyFile, serverCertFile, serverAddress, serverName)

	expectedErrorMessage := "remote error: tls: bad certificate"
	require.Error(t, clientErr)
	require.Equal(t, expectedErrorMessage, clientErr.Error())

	defer func() {
		closeErr := server.Close()
		require.NoError(t, closeErr)
	}()
}
