package main

import (
	"github.com/stretchr/testify/require"
	"tcplb/lib/core"
	"testing"
)

func TestUpstreamListValueErrorHelp(t *testing.T) {
	v := &UpstreamListValue{
		Upstreams: make([]core.Upstream, 0),
	}
	err := v.Set("localhost:443,127.*.*.*,127.0.0.1:9021")
	require.Error(t, err)
	require.Equal(t, "expected upstream address of form host:port but got 127.*.*.*", err.Error())
}
