package bridge

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/confighubai/flux-bridge/internal/controller"
)

func TestFluxBridge(t *testing.T) {
	t.Parallel()

	name := "test"
	bridge, err := NewFluxBridge(controller.FluxController{}, name)
	require.NoError(t, err)
	require.Equal(t, name, bridge.name)
}
