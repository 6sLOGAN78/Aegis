package identity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatewayIDFromSPIFFE(t *testing.T) {
	id, err := GatewayIDFromSPIFFE("spiffe://aegis.local/ns/gateway/sa/aegis-gateway/gw/gateway-1")
	require.NoError(t, err)
	assert.Equal(t, "gateway-1", id)

	bad := []string{
		"spiffe://aegis.local/ns/gateway/sa/aegis-gateway",
		"spiffe://other.local/ns/gateway/sa/aegis-gateway/gw/gateway-1",
		GatewaySPIFFEPrefix,
		GatewaySPIFFEPrefix + strings.Repeat("a", 65),
		GatewaySPIFFEPrefix + "a/b",
		GatewaySPIFFEPrefix + "a b",
		GatewaySPIFFEPrefix + "a%2fb",
		GatewaySPIFFEPrefix + "..%2f",
		"",
	}
	for _, b := range bad {
		_, err := GatewayIDFromSPIFFE(b)
		assert.ErrorIs(t, err, ErrNotGatewayIdentity, b)
	}
	_, err = GatewayIDFromSPIFFE(GatewaySPIFFEPrefix + strings.Repeat("a", 64))
	assert.NoError(t, err)
}

func TestGatewaySPIFFEIDRoundTrip(t *testing.T) {
	s := GatewaySPIFFEID("aegis-gateway-0")
	assert.Equal(t, "spiffe://aegis.local/ns/gateway/sa/aegis-gateway/gw/aegis-gateway-0", s)
	id, err := GatewayIDFromSPIFFE(s)
	require.NoError(t, err)
	assert.Equal(t, "aegis-gateway-0", id)
}
