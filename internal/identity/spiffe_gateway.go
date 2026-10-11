package identity

import (
	"errors"
	"regexp"
	"strings"
)

// ErrNotGatewayIdentity is returned when a SPIFFE id is not a per-replica
// gateway identity.
var ErrNotGatewayIdentity = errors.New("spiffe id is not a gateway replica identity")

// GatewaySPIFFEPrefix is the canonical prefix of per-replica gateway ids.
const GatewaySPIFFEPrefix = "spiffe://aegis.local/ns/gateway/sa/aegis-gateway/gw/"

var gatewayIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// GatewaySPIFFEID builds the canonical per-replica SPIFFE id for a gateway id.
func GatewaySPIFFEID(id string) string {
	return GatewaySPIFFEPrefix + id
}

// GatewayIDFromSPIFFE extracts the gateway id from a canonical per-replica
// SPIFFE id (output of ExtractSPIFFEID). The bare backend-facing gateway id
// and any other shape return ErrNotGatewayIdentity.
func GatewayIDFromSPIFFE(spiffe string) (string, error) {
	rest, ok := strings.CutPrefix(spiffe, GatewaySPIFFEPrefix)
	if !ok || !gatewayIDPattern.MatchString(rest) {
		return "", ErrNotGatewayIdentity
	}
	return rest, nil
}
