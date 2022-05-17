package forwarder

import (
	"context"
	"github.com/stretchr/testify/require"
	"tcplb/lib/core"
	"tcplb/lib/slog"
	"testing"
)

func TestClientIDFromContext(t *testing.T) {
	parentCtx := context.Background()
	c := core.ClientID{Namespace: "handler-test", Key: "a"}
	childCtx := NewContextWithClientID(parentCtx, c)
	cPrime, ok := ClientIDFromContext(childCtx)
	require.True(t, ok)
	require.Equal(t, c, cPrime)
}

func TestClientIDFromContextMissing(t *testing.T) {
	ctx := context.Background()
	_, ok := ClientIDFromContext(ctx)
	require.False(t, ok)
}

func TestUpstreamsFromContext(t *testing.T) {
	a := core.Upstream{Network: "handler-test", Address: "a"}
	b := core.Upstream{Network: "handler-test", Address: "b"}
	c := core.Upstream{Network: "handler-test", Address: "c"}
	upstreams := core.NewUpstreamSet(a, b, c)

	parentCtx := context.Background()
	childCtx := NewContextWithUpstreams(parentCtx, upstreams)
	upstreamsPrime, ok := UpstreamsFromContext(childCtx)
	require.True(t, ok)
	require.Equal(t, upstreams, upstreamsPrime)
}

func TestUpstreamsFromContextMissing(t *testing.T) {
	ctx := context.Background()
	_, ok := UpstreamsFromContext(ctx)
	require.False(t, ok)
}

func TestClientIDAndUpstreamsFromContext(t *testing.T) {
	// test that one context key doesn't overwrite the other one...

	clientID := core.ClientID{Namespace: "handler-test", Key: "a"}

	a := core.Upstream{Network: "handler-test", Address: "a"}
	b := core.Upstream{Network: "handler-test", Address: "b"}
	c := core.Upstream{Network: "handler-test", Address: "c"}
	upstreams := core.NewUpstreamSet(a, b, c)

	parentCtx := context.Background()

	childCtx := NewContextWithUpstreams(
		NewContextWithClientID(parentCtx, clientID),
		upstreams)

	clientIDPrime, ok := ClientIDFromContext(childCtx)
	require.True(t, ok)
	require.Equal(t, clientID, clientIDPrime)

	upstreamsPrime, ok := UpstreamsFromContext(childCtx)
	require.True(t, ok)
	require.Equal(t, upstreams, upstreamsPrime)
}

// alwaysPanicHandler always panics when asked to Handle.
type alwaysPanicHandler struct {
	PanicValue string
}

func (h alwaysPanicHandler) Handle(ctx context.Context, conn AuthenticatedConn) {
	panic(h.PanicValue)
}

func TestRecovererHandlerLogsPanics(t *testing.T) {
	logger := &slog.RecordingLogger{}

	h := &RecovererHandler{
		Logger: logger,
		Inner:  alwaysPanicHandler{PanicValue: "oh no!"},
	}

	ctx := context.Background()
	var conn AuthenticatedConn

	h.Handle(ctx, conn)

	require.Len(t, logger.Events, 1)
	event := logger.Events[0]
	require.Equal(t, "error", event.Level)
	require.Equal(t, "RecovererHandler: Unexpected panic!", event.Msg)
	require.Equal(t, "oh no!", event.Details)
}
