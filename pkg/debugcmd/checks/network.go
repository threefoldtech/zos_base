package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	cnins "github.com/containernetworking/plugins/pkg/ns"
	"github.com/threefoldtech/zos_base/pkg"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
	"github.com/threefoldtech/zos_base/pkg/network/namespace"
	"github.com/threefoldtech/zos_base/pkg/network/nr"
	"github.com/threefoldtech/zos_base/pkg/versioned"
	"github.com/threefoldtech/zos_base/pkg/zinit"
	"github.com/vishvananda/netlink"
)

const (
	networkdVolatileDir = "/var/run/cache/networkd"
	networksDir         = "networks"
	myceliumKeyDir      = "mycelium-key"
)

type NetworkChecker struct {
	netID      zos.NetID
	nsName     string
	netCfgPath string
	nrr        *nr.NetResource
}

func (nc *NetworkChecker) Name() string { return "network" }

func (nc *NetworkChecker) Run(ctx context.Context, data *CheckData) []HealthCheck {
	netID := zos.NetworkID(data.Twin, data.Workload.Name)
	nc.netID = netID
	nc.nsName = data.Network(ctx, netID)
	nc.netCfgPath = filepath.Join(networkdVolatileDir, networksDir, netID.String())
	nc.nrr = nr.New(pkg.Network{NetID: netID}, filepath.Join(networkdVolatileDir, myceliumKeyDir))

	return []HealthCheck{
		nc.checkConfig(),
		nc.checkNamespace(),
		nc.checkInterfaces(),
		nc.checkBridge(),
		nc.checkMycelium(),
	}
}

func (nc *NetworkChecker) checkConfig() HealthCheck {
	_, raw, err := versioned.ReadFile(nc.netCfgPath)
	if err != nil {
		return failure("network.config", fmt.Sprintf("config file not found: %v", err), map[string]interface{}{"path": nc.netCfgPath, "netid": nc.netID.String()})
	}

	var netCfg pkg.Network
	if err := json.Unmarshal(raw, &netCfg); err != nil {
		return failure("network.config", fmt.Sprintf("config file invalid: %v", err), map[string]interface{}{"path": nc.netCfgPath, "netid": nc.netID.String()})
	}

	if netCfg.NetID != nc.netID {
		return failure("network.config", fmt.Sprintf("netid mismatch: expected %s, got %s", nc.netID.String(), netCfg.NetID.String()), map[string]interface{}{"expected": nc.netID.String(), "got": netCfg.NetID.String()})
	}

	return success("network.config", "config valid", map[string]interface{}{"path": nc.netCfgPath, "netid": nc.netID.String()})
}

func (nc *NetworkChecker) checkNamespace() HealthCheck {
	if !namespace.Exists(nc.nsName) {
		return failure("network.namespace", "namespace not found", map[string]interface{}{"namespace": nc.nsName})
	}
	return success("network.namespace", "namespace exists", map[string]interface{}{"namespace": nc.nsName})
}

func (nc *NetworkChecker) checkInterfaces() HealthCheck {
	wgIface, _ := nc.nrr.WGName()
	nrIface, _ := nc.nrr.NRIface()
	pubIface := "public"

	netnsLinks := map[string]struct{}{}
	if netNS, err := namespace.GetByName(nc.nsName); err == nil {
		_ = netNS.Do(func(_ cnins.NetNS) error {
			links, err := netlink.LinkList()
			if err == nil {
				for _, l := range links {
					netnsLinks[l.Attrs().Name] = struct{}{}
				}
			}
			return nil
		})
		netNS.Close()
	}

	missing := []string{}
	for _, iface := range []string{wgIface, nrIface, pubIface} {
		if _, ok := netnsLinks[iface]; !ok {
			missing = append(missing, iface)
		}
	}

	if len(missing) > 0 {
		return failure("network.interfaces", fmt.Sprintf("missing interfaces: %v", missing), map[string]interface{}{"namespace": nc.nsName, "missing": missing})
	}

	return success("network.interfaces", "all required interfaces present", map[string]interface{}{"namespace": nc.nsName})
}

func (nc *NetworkChecker) checkBridge() HealthCheck {
	brName, _ := nc.nrr.BridgeName()
	brPath := filepath.Join("/sys/class/net", brName)

	if _, err := os.Stat(brPath); err != nil {
		return failure("network.bridge", fmt.Sprintf("bridge not found: %v", err), map[string]interface{}{"bridge": brName})
	}

	brifDir := filepath.Join(brPath, "brif")
	ents, err := os.ReadDir(brifDir)
	if err != nil || len(ents) == 0 {
		return failure("network.bridge", fmt.Sprintf("bridge has no members: %v", err), map[string]interface{}{"bridge": brName})
	}

	return success("network.bridge", "bridge has members", map[string]interface{}{"bridge": brName})
}

func (nc *NetworkChecker) checkMycelium() HealthCheck {
	service := nc.nrr.MyceliumServiceName()
	st, err := zinit.Default().Status(service)
	if err != nil {
		return failure("network.mycelium", fmt.Sprintf("cannot get service status: %v", err), map[string]interface{}{"service": service})
	}

	if !st.State.Is(zinit.ServiceStateRunning) {
		return failure("network.mycelium", fmt.Sprintf("service not running: %s", st.State.String()), map[string]interface{}{"service": service, "state": st.State.String()})
	}

	return success("network.mycelium", "service running", map[string]interface{}{"service": service, "pid": st.Pid})
}

var NetworkCheckerInstance = &NetworkChecker{}
