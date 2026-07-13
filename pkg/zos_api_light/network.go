package zosapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	gridtypes "github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_sdk_go/rmb-sdk-go/peer"
)

func (g *ZosAPI) networkInterfacesHandler(ctx context.Context, payload []byte) (interface{}, error) {
	results := make(map[string][]net.IP)
	interfaces, err := g.networkerLightStub.Interfaces(ctx, "zos", "")
	if err != nil {
		return nil, fmt.Errorf("failed to get ips for 'zos' interface: %w", err)
	}
	zosIfcIps := interfaces.Interfaces["zos"].IPs
	results["zos"] = make([]net.IP, len(zosIfcIps))
	for i, ip := range zosIfcIps {
		results["zos"][i] = ip.IP
	}

	return results, nil
}

func (g *ZosAPI) networkHasIPv6Handler(ctx context.Context, payload []byte) (interface{}, error) {
	// networkd light
	return false, nil
}

func (g *ZosAPI) networkListPrivateIPsHandler(ctx context.Context, payload []byte) (interface{}, error) {
	var args struct {
		NetworkName gridtypes.Name `json:"network_name"`
	}
	if err := json.Unmarshal(payload, &args); err != nil {
		return nil, err
	}
	twin := peer.GetTwinID(ctx)
	return g.provisionStub.ListPrivateIPs(ctx, twin, args.NetworkName)
}

func (g *ZosAPI) networkListWGPortsHandler(ctx context.Context, payload []byte) (interface{}, error) {
	return g.networkerLightStub.WireguardPorts(ctx)
}

func (g *ZosAPI) networkPublicConfigGetHandler(ctx context.Context, payload []byte) (interface{}, error) {
	return g.networkerLightStub.LoadPublicConfig(ctx)
}
