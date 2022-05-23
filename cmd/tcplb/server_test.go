package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
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
 *
 * - some application behaviour and test behaviour is timing dependent. If the
 *   machine running this test suite is under heavy load or has difficulty
 *   scheduling goroutine execution, we may see false positive test failures.
 */

const (
	/* demo application protocol:
	 * client side
	 * - send ApplicationClientHello
	 * - await ApplicationServerHello
	 * - send ApplicationClientHello
	 * - await ApplicationServerHello
	 *
	 * server side
	 * - await ApplicationClientHello
	 * - send ApplicationServerHello
	 * - await ApplicationClientHello
	 * - send ApplicationServerHello
	 *
	 * two roundtrips gives us a point half-way where we know
	 * both the client and server have done something
	 */
	ApplicationClientHello   = `HEY THERE I AM CLIENT\n`
	ApplicationServerHello   = `OH HEY THERE I AM SERVER\n`
	ApplicationClientGoodbye = `GOODBYE FROM CLIENT\n`
	ApplicationServerGoodbye = `GOODBYE FROM SERVER\n`
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
		TLSHandshakeTimeout:     2 * time.Second,
		MaxConnectionsPerClient: 25,
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

	mu                 sync.Mutex
	currentConnections int
	peakConnections    int
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

func (s *demoAppServer) PeakConnectionCount() int {
	s.mu.Lock()
	result := s.peakConnections
	s.mu.Unlock()
	return result
}

func (s *demoAppServer) incConnectionCount() {
	s.mu.Lock()
	s.currentConnections += 1
	if s.currentConnections > s.peakConnections {
		s.peakConnections = s.currentConnections
	}
	s.mu.Unlock()
}

func (s *demoAppServer) decConnectionCount() {
	s.mu.Lock()
	s.currentConnections -= 1
	s.mu.Unlock()
}

func (s *demoAppServer) Serve() error {
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return err
			}
		}
		go func() {
			s.incConnectionCount()
			s.HandleFunc(conn)
			s.decConnectionCount()
		}()
	}
}

func demoHandleFunc(conn net.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	helloBuffer := make([]byte, len(ApplicationClientHello))
	_, err := io.ReadFull(conn, helloBuffer)
	if err != nil {
		return
	}
	_, err = conn.Write([]byte(ApplicationServerHello))
	if err != nil {
		return
	}
	goodbyeBuffer := make([]byte, len(ApplicationClientGoodbye))
	_, err = io.ReadFull(conn, goodbyeBuffer)
	if err != nil {
		return
	}
	_, err = conn.Write([]byte(ApplicationServerGoodbye))
	if err != nil {
		return
	}
}

func (s *demoAppServer) Close() error {
	return s.Listener.Close()
}

func dialDemoTLSConn(ctx context.Context, certFile, keyFile, rootCAPath, serverAddress, serverName string) (*tls.Conn, error) {
	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != err {
		return nil, err
	}
	rootCAs, err := loadRootCAs(rootCAPath)
	if err != nil {
		return nil, err
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
	return tls.DialWithDialer(d, "tcp", serverAddress, tlsConfig)
}

func demoAppWrite(conn io.Writer, msg []byte) error {
	written, err := conn.Write(msg)
	if err != nil {
		return err
	}
	if written != len(msg) {
		return errors.New(fmt.Sprintf("failed to send msg %s", string(msg)))
	}
	return nil
}

func demoAppWriteApplicationClientHello(conn io.Writer) error {
	return demoAppWrite(conn, []byte(ApplicationClientHello))
}

func demoAppWriteApplicationClientGoodbye(conn io.Writer) error {
	return demoAppWrite(conn, []byte(ApplicationClientGoodbye))
}

func demoAppReadApplicationServerHello(conn io.Reader) error {
	buffer := make([]byte, len(ApplicationServerHello))
	_, err := io.ReadFull(conn, buffer)
	if err != nil {
		return err
	}
	if !bytes.Equal(buffer[:len(ApplicationServerHello)], []byte(ApplicationServerHello)) {
		return errors.New("did not receive expected ApplicationServerHello")
	}
	return nil
}

func demoAppReadApplicationServerGoodbye(conn io.Reader) error {
	buffer := make([]byte, len(ApplicationServerGoodbye))
	_, err := io.ReadFull(conn, buffer)
	if err != nil {
		return err
	}
	if !bytes.Equal(buffer[:len(ApplicationServerGoodbye)], []byte(ApplicationServerGoodbye)) {
		return errors.New("did not receive expected ApplicationServerGoodbye")
	}
	return nil
}

func makeDemoAppRequestTLS(ctx context.Context, certFile, keyFile, rootCAPath, serverAddress, serverName string) error {
	tlsConn, err := dialDemoTLSConn(ctx, certFile, keyFile, rootCAPath, serverAddress, serverName)
	if err != nil {
		return err
	}
	defer func() { _ = tlsConn.Close() }()
	if err = demoAppWriteApplicationClientHello(tlsConn); err != nil {
		return err
	}
	if err = demoAppReadApplicationServerHello(tlsConn); err != nil {
		return err
	}
	if err = demoAppWriteApplicationClientGoodbye(tlsConn); err != nil {
		return err
	}
	return demoAppReadApplicationServerGoodbye(tlsConn)
}

func makeDemoAppRequestTCP(ctx context.Context, serverAddress string) error {
	d := &net.Dialer{}
	tcpConn, err := d.Dial("tcp", serverAddress)
	if err != nil {
		return err
	}
	defer func() { _ = tcpConn.Close() }()
	if err = demoAppWriteApplicationClientHello(tcpConn); err != nil {
		return err
	}
	if err = demoAppReadApplicationServerHello(tcpConn); err != nil {
		return err
	}
	if err = demoAppWriteApplicationClientGoodbye(tcpConn); err != nil {
		return err
	}
	return demoAppReadApplicationServerGoodbye(tcpConn)
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
		logger.Error(&slog.LogRecord{Msg: "upstreamServer.Serve returned error", Error: srvErr})
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

	// start the load balancer server
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
		logger.Error(&slog.LogRecord{Msg: "upstreamServer.Serve returned error", Error: srvErr})
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

	// start the load balancer server
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
		logger.Error(&slog.LogRecord{Msg: "upstreamServer.Serve returned error", Error: srvErr})
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

	// start the load balancer server
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
		logger.Error(&slog.LogRecord{Msg: "upstreamServer.Serve returned error", Error: srvErr})
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

	// start the load balancer server
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

func makeDemoAppRequestSynchronisedTLS(ctx context.Context, certFile, keyFile, rootCAPath, serverAddress,
	serverName string, readyWG *sync.WaitGroup, goWG *sync.WaitGroup, out chan<- error) {
	tlsConn, err := dialDemoTLSConn(ctx, certFile, keyFile, rootCAPath, serverAddress, serverName)
	if err != nil {
		out <- err
		return
	}
	defer func() { _ = tlsConn.Close() }()

	if err = demoAppWriteApplicationClientHello(tlsConn); err != nil {
		readyWG.Done()
		out <- err
		return
	}
	if err = demoAppReadApplicationServerHello(tlsConn); err != nil {
		readyWG.Done()
		out <- err
		return
	}
	// wait for the signal to continue
	readyWG.Done()
	goWG.Wait()

	if err = demoAppWriteApplicationClientGoodbye(tlsConn); err != nil {
		out <- err
		return
	}
	out <- demoAppReadApplicationServerGoodbye(tlsConn)
}

func TestServerBalancesConnections(t *testing.T) {
	logger := slog.GetDefaultLogger()

	serverName := "tcplb-server-strong"
	serverCertFile := testResource(t, "tcplb-server-strong/cert.pem")
	serverKeyFile := testResource(t, "tcplb-server-strong/key.pem")

	clientName := "client-strong"
	clientId := core.ClientID{Namespace: authn.DefaultNamespace, Key: clientName}
	clientCertFile := testResource(t, "client-strong/cert.pem")
	clientKeyFile := testResource(t, "client-strong/key.pem")

	// launch a pair of demo app servers to act as upstreams
	upstreamServer1, err := newDemoAppServer("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		srvErr := upstreamServer1.Serve()
		logger.Error(&slog.LogRecord{Msg: "upstreamServer1.Serve returned error", Error: srvErr})
	}()
	defer func() {
		_ = upstreamServer1.Close()
	}()
	upstreamServer2, err := newDemoAppServer("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		srvErr := upstreamServer2.Serve()
		logger.Error(&slog.LogRecord{Msg: "upstreamServer2.Serve returned error", Error: srvErr})
	}()
	defer func() {
		_ = upstreamServer2.Close()
	}()

	// Configure tcplb server to:
	// - forward to both upstreams
	// - trust the client
	upstream1 := core.Upstream{Network: "tcp", Address: upstreamServer1.Listener.Addr().String()}
	upstream2 := core.Upstream{Network: "tcp", Address: upstreamServer2.Listener.Addr().String()}
	upstreams := []core.Upstream{
		upstream1, upstream2,
	}
	config := newTestServerConfig(serverCertFile, serverKeyFile, clientCertFile, upstreams, clientId)

	server, err := NewServer(logger, config)
	require.NoError(t, err, err)

	serverAddress := server.Listener.Addr().String()

	// start the load balancer server
	go func() {
		srvErr := server.Serve()
		if srvErr != nil {
			logger.Error(&slog.LogRecord{Msg: "server.Serve returned error", Error: srvErr})
		}
	}()

	ctx := context.Background()
	// Start a number of demo app clients. Synchronise them, so they all send their
	// app client hello message then wait for the signal to read the server's app
	// server hello message. This ensures all connections to the load balancer are
	// active at the one time, to exercise the min-connections load balancing policy.
	clientCount := 10

	readyWG := &sync.WaitGroup{}
	goWG := &sync.WaitGroup{}
	goWG.Add(1)

	out := make(chan error, clientCount)

	for i := 0; i < clientCount; i++ {
		readyWG.Add(1)
		go makeDemoAppRequestSynchronisedTLS(ctx, clientCertFile, clientKeyFile, serverCertFile, serverAddress,
			serverName, readyWG, goWG, out)
	}
	readyWG.Wait()
	goWG.Done()

	for i := 0; i < clientCount; i++ {
		clientErr := <-out
		require.NoError(t, clientErr)
	}

	// FIXME there's likely still some sloppiness here in how the
	// synchronisation works. Maybe this could be more reliable if
	// we synchronised the upstreams as well from this test.
	expectedPeakConnections := clientCount / 2
	peak1 := upstreamServer2.PeakConnectionCount()
	peak2 := upstreamServer2.PeakConnectionCount()
	require.InDelta(t, expectedPeakConnections, peak1, 1.1)
	require.InDelta(t, expectedPeakConnections, peak2, 1.1)

	defer func() {
		closeErr := server.Close()
		require.NoError(t, closeErr)
	}()
}
