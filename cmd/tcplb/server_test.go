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
	"time"
)

/* This is a heavyweight suite of tests that tests the entire server.
 * These tests require that the TCPLB_TESTBED_ROOT is set to the testbed
 * directory, containing various example server and client certificates.
 *
 * USAGE:
 *
 * 1. ensure working dir is root directory of the repo checkout
 * 2. `make servertest`
 *
 * KNOWN ISSUES:
 * - this suite launches various clients and servers, but doesn't set
 *   timeouts. If the application or test suite are defective, some tests
 *   may hang. This can be mitigated with the `-timeout d` flag to
 *   go test, see `go help testflags`, but it'd be good to fix it.
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
		TLSHandshakeTimeout:     2 * time.Second,
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

// makeSilentTCPClientConn establishes a TCP connection to given server then says nothing.
// it can be terminated by the context.
func makeSilentTCPClientConn(ctx context.Context, serverAddress string, out chan<- error) {
	d := &net.Dialer{}
	network := "tcp"
	tcpConn, err := d.Dial(network, serverAddress)
	if err != nil {
		out <- err
		return
	}
	defer func() { _ = tcpConn.Close() }()

	// poll the conn to sense if the load balancer closes it.
	buff := make([]byte, 1)
	for {
		err := tcpConn.SetDeadline(time.Now().Add(10 * time.Millisecond))
		if err != nil {
			out <- err
			return
		}
		// stopping condition 1: EOF
		_, err = tcpConn.Read(buff)
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			out <- err
			return
		}
		// stopping condition 2: context cancellation
		if ctx.Err() != nil {
			out <- err
			return
		}
	}
}

// This is a slow test, it sleeps for 2 seconds.
func TestServerRejectsSilentTCPClient(t *testing.T) {
	// This tests a scenario where a client opens a TCP connection and then
	// writes no bytes - the server is expected to enforce a TLS handshake timeout
	// and close the connection.
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

	clientCtx, clientCancel := context.WithCancel(ctx)
	defer clientCancel()

	// Asynchronously open a client TCP connection to server, then have the client nothing.
	// Don't attempt to do TLS handshake. See if server times out and closes the conn.
	clientOut := make(chan error, 1)
	go makeSilentTCPClientConn(clientCtx, serverAddress, clientOut)

	timer := time.NewTimer(config.TLSHandshakeTimeout * 2)
	select {
	case <-timer.C:
		require.Fail(t, "expected server TLSHandshakeTimeout to have kicked in")
	case clientErr := <-clientOut:
		require.ErrorIs(t, clientErr, io.EOF)
	}

	defer func() {
		closeErr := server.Close()
		require.NoError(t, closeErr)
	}()
}
