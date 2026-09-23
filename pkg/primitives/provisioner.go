package primitives

import (
	"github.com/threefoldtech/zbus"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
	"github.com/threefoldtech/zos_base/pkg/kernel"
	"github.com/threefoldtech/zos_base/pkg/primitives/gateway"
	"github.com/threefoldtech/zos_base/pkg/primitives/network"
	netlight "github.com/threefoldtech/zos_base/pkg/primitives/network-light"
	"github.com/threefoldtech/zos_base/pkg/primitives/pubip"
	"github.com/threefoldtech/zos_base/pkg/primitives/qsfs"
	"github.com/threefoldtech/zos_base/pkg/primitives/vm"
	vmlight "github.com/threefoldtech/zos_base/pkg/primitives/vm-light"
	"github.com/threefoldtech/zos_base/pkg/primitives/volume"
	"github.com/threefoldtech/zos_base/pkg/primitives/zdb"
	"github.com/threefoldtech/zos_base/pkg/primitives/zlogs"
	"github.com/threefoldtech/zos_base/pkg/primitives/zmount"
	"github.com/threefoldtech/zos_base/pkg/provision"
)

// NewPrimitivesProvisioner creates a new 0-OS provisioner
//
// Only the workload types this node variant can actually serve are registered.
// A manager reaches its worker over zbus, and a light node runs netlightd
// (module "netlight") in place of networkd (module "network"), so registering
// the full network backed types there would leave a request queued for a module
// that never answers. The provisioner rejects an unregistered type outright,
// which surfaces to the owner as a failed workload instead of a deployment that
// never resolves. Keep this in step with systemMonitor.GetNodeFeatures, which
// advertises the same split to the grid.
func NewPrimitivesProvisioner(zbus zbus.Client) provision.Provisioner {
	return provision.NewMapProvisioner(managersFor(zbus, kernel.GetParams()))
}

// managersFor builds the set of workload managers the given node variant can
// actually serve. Split out from NewPrimitivesProvisioner so the variant gating
// is testable without a matching /proc/cmdline.
func managersFor(zbus zbus.Client, params kernel.Params) map[gridtypes.WorkloadType]provision.Manager {
	// types every variant serves. their managers talk to storaged, flistd,
	// contd and vmd, which run everywhere.
	managers := map[gridtypes.WorkloadType]provision.Manager{
		zos.ZMountType:           zmount.NewManager(zbus),
		zos.ZLogsType:            zlogs.NewManager(zbus),
		zos.QuantumSafeFSType:    qsfs.NewManager(zbus),
		zos.ZDBType:              zdb.NewManager(zbus),
		zos.VolumeType:           volume.NewManager(zbus),
		zos.GatewayNameProxyType: gateway.NewNameManager(zbus),
		zos.GatewayFQDNProxyType: gateway.NewFQDNManager(zbus),
		zos.PublicIPType:         pubip.NewManager(zbus),
		zos.PublicIPv4Type:       pubip.NewManager(zbus), // backward compatibility
	}

	if params.IsLight() {
		// served by netlightd, module "netlight". networkd does not run here.
		managers[zos.NetworkLightType] = netlight.NewManager(zbus)
		managers[zos.ZMachineLightType] = vmlight.NewManager(zbus)
	} else {
		// served by networkd, module "network". netlightd does not run here.
		managers[zos.NetworkType] = network.NewManager(zbus)
		managers[zos.ZMachineType] = vm.NewManager(zbus)
	}

	return managers
}
