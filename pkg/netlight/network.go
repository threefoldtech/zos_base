package netlight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/cenkalti/backoff"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg"
	"github.com/threefoldtech/zos_base/pkg/cache"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
	"github.com/threefoldtech/zos_base/pkg/netbase/wireguard"
	"github.com/threefoldtech/zos_base/pkg/netlight/bridge"
	"github.com/threefoldtech/zos_base/pkg/netlight/ifaceutil"
	"github.com/threefoldtech/zos_base/pkg/netlight/ipam"
	"github.com/threefoldtech/zos_base/pkg/netlight/namespace"
	"github.com/threefoldtech/zos_base/pkg/netlight/options"
	"github.com/threefoldtech/zos_base/pkg/netlight/public"
	"github.com/threefoldtech/zos_base/pkg/netlight/resource"
	"github.com/threefoldtech/zos_base/pkg/set"
	"github.com/threefoldtech/zos_base/pkg/versioned"
	"github.com/vishvananda/netlink"
)

const (
	NDMZBridge    = "br-ndmz"
	NDMZGw        = "gw"
	mib           = 1024 * 1024
	ipamLeaseDir  = "ndmz-lease"
	DefaultBridge = "zos"
	networkDir    = "networks"
	linkDir       = "link"
)

var NDMZGwIP = &net.IPNet{
	IP:   net.ParseIP("100.127.0.1"),
	Mask: net.CIDRMask(16, 32),
}

var NetworkSchemaLatestVersion = semver.MustParse("0.1.0")

type networker struct {
	ipamLease   string
	networkDir  string
	portSet     *set.UIntSet
	linkDirPath string
}

var _ pkg.NetworkerLight = (*networker)(nil)

func NewNetworker() (pkg.NetworkerLight, error) {
	vd, err := cache.VolatileDir("networkd", 50*mib)
	if err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("failed to create networkd cache directory: %w", err)
	}

	ipamLease := filepath.Join(vd, ipamLeaseDir)
	runtimeDir := filepath.Join(vd, networkDir)
	linkDirPath := filepath.Join(runtimeDir, linkDir)

	if err := os.MkdirAll(linkDirPath, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create directory: '%s'", linkDirPath)
	}

	n := networker{
		ipamLease:   ipamLease,
		networkDir:  runtimeDir,
		portSet:     set.NewInt(),
		linkDirPath: linkDirPath,
	}

	if err := n.syncWGPorts(); err != nil {
		return nil, err
	}
	return &n, nil
}

func (n *networker) Create(name string, wl gridtypes.WorkloadID, net zos.NetworkLight) error {
	if err := n.storeNetwork(name, wl, net); err != nil {
		return errors.Wrap(err, "failed to store network object")
	}

	b, err := bridge.Get(NDMZBridge)
	if err != nil {
		return err
	}
	ip, err := ipam.AllocateIPv4(name, n.ipamLease)
	if err != nil {
		return err
	}

	cleanup := func() {
		log.Error().Msg("clean up network resource")
		if err := resource.Delete(name); err != nil {
			log.Error().Err(err).Msg("error during deletion of network resource after failed deployment")
		}
		if err := n.releasePort(net.WGListenPort); err != nil {
			log.Error().Err(err).Msg("release wireguard port failed")
		}
	}

	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	netr, err := resource.Create(name, b, ip, NDMZGwIP, &net.Subnet.IPNet, net)
	if err != nil {
		return err
	}

	return n.setupWireguard(name, net, netr)
}

func (n *networker) Delete(name string) error {
	if err := ipam.DeAllocateIPv4(name, n.ipamLease); err != nil {
		return err
	}

	netNR, err := n.networkOf(pkg.NetID(name))
	if err != nil {
		return err
	}

	if err := n.releasePort(netNR.WGListenPort); err != nil {
		log.Error().Err(err).Msg("release wireguard port failed")
	}

	if err := resource.Delete(name); err != nil {
		return err
	}

	path := filepath.Join(n.networkDir, name)
	return os.Remove(path)
}

func (n *networker) AttachPrivate(name, id string, vmIp net.IP) (device pkg.TapDevice, err error) {
	resource, err := resource.Get(name)
	if err != nil {
		return
	}
	return resource.AttachPrivate(id, vmIp)
}

func (n *networker) AttachMycelium(name, id string, seed []byte) (device pkg.TapDevice, err error) {
	resource, err := resource.Get(name)
	if err != nil {
		return
	}
	return resource.AttachMycelium(id, seed)
}

// detach everything for this id
func (n *networker) Detach(id string) error {
	// delete all tap devices for both mycelium and priv (if exists)
	deviceName := ifaceutil.DeviceNameFromInputBytes([]byte(id))
	myName := fmt.Sprintf("m-%s", deviceName)

	if err := ifaceutil.Delete(myName, nil); err != nil {
		return err
	}

	tapName := fmt.Sprintf("b-%s", deviceName)

	return ifaceutil.Delete(tapName, nil)
}

func (n *networker) AttachZDB(id string) (string, error) {
	name := ifaceutil.DeviceNameFromInputBytes([]byte(id))
	nsName := n.Namespace(name)

	ns, err := namespace.GetByName(nsName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if ns == nil {
		ns, err = namespace.Create(nsName)
		if err != nil {
			return "", err
		}
	}

	r, err := resource.Get("dmz")
	if err != nil {
		return "", err
	}

	return nsName, r.AttachMyceliumZDB(id, ns)
}

// GetSubnet of a local network resource identified by the network ID, ipv4 and ipv6
// subnet respectively
func (n *networker) GetSubnet(networkID pkg.NetID) (net.IPNet, error) {
	localNR, err := n.networkOf(networkID)
	if err != nil {
		return net.IPNet{}, errors.Wrapf(err, "couldn't load network with name (%s)", networkID.String())
	}

	return localNR.Subnet.IPNet, nil
}

func (n *networker) networkOf(id pkg.NetID) (nr pkg.Network, err error) {
	path := filepath.Join(n.networkDir, id.String())
	file, err := os.OpenFile(path, os.O_RDWR, 0660)
	if err != nil {
		return nr, err
	}
	defer file.Close()

	reader, err := versioned.NewReader(file)
	if versioned.IsNotVersioned(err) {
		// old data that doesn't have any version information
		if _, err := file.Seek(0, 0); err != nil {
			return nr, err
		}

		reader = versioned.NewVersionedReader(NetworkSchemaLatestVersion, file)
	} else if err != nil {
		return nr, err
	}

	var net pkg.Network
	dec := json.NewDecoder(reader)

	version := reader.Version()
	// validV1 := versioned.MustParseRange(fmt.Sprintf("=%s", pkg.NetworkSchemaV1))
	validLatest := versioned.MustParseRange(fmt.Sprintf("<=%s", NetworkSchemaLatestVersion.String()))

	if validLatest(version) {
		if err := dec.Decode(&net); err != nil {
			return nr, err
		}
	} else {
		return nr, fmt.Errorf("unknown network object version (%s)", version)
	}

	return net, nil
}

func (n *networker) ZDBIPs(zdbNamespace string) ([]net.IP, error) {
	ips := make([]net.IP, 0)

	netNs, err := namespace.GetByName(zdbNamespace)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	err = netNs.Do(func(_ ns.NetNS) error {
		links, err := netlink.LinkList()
		if err != nil {
			return err
		}
		for _, link := range links {
			addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
			if err != nil {
				return err
			}
			for _, addr := range addrs {
				ips = append(ips, addr.IP)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return ips, nil
}

func (n *networker) Ready() error {
	return nil
}

func (n *networker) Namespace(id string) string {
	return fmt.Sprintf("n%s", id)
}

func (n *networker) ZOSAddresses(ctx context.Context) <-chan pkg.NetlinkAddresses {
	var index int
	_ = backoff.Retry(func() error {
		link, err := netlink.LinkByName(DefaultBridge)
		if err != nil {
			log.Error().Err(err).Msg("can't get defaut bridge")
			return err
		}
		index = link.Attrs().Index
		return nil
	}, backoff.NewConstantBackOff(2*time.Second))

	get := func() pkg.NetlinkAddresses {
		var result pkg.NetlinkAddresses
		link, err := netlink.LinkByName(DefaultBridge)
		if err != nil {
			log.Error().Err(err).Msgf("could not find the '%s' bridge", DefaultBridge)
			return nil
		}
		values, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			log.Error().Err(err).Msgf("could not list the '%s' bridge ips", DefaultBridge)
			return nil
		}
		for _, value := range values {
			result = append(result, *value.IPNet)
		}

		slices.SortFunc(result, func(a, b net.IPNet) int {
			return bytes.Compare(a.IP, b.IP)
		})

		return result
	}

	updateChan := make(chan netlink.AddrUpdate)
	if err := netlink.AddrSubscribe(updateChan, ctx.Done()); err != nil {
		log.Error().Err(err).Msgf("could not subscribe to addresses updates")
		return nil
	}

	ch := make(chan pkg.NetlinkAddresses)
	var current pkg.NetlinkAddresses
	go func() {
		defer close(ch)

		for {
			select {
			case <-ctx.Done():
				return
			case update := <-updateChan:
				if update.LinkIndex != index || !update.NewAddr {
					continue
				}
				new := get()
				if slices.CompareFunc(current, new, func(a, b net.IPNet) int {
					return bytes.Compare(a.IP, b.IP)
				}) == 0 {
					// if the 2 sets of IPs are identitcal, we don't
					// trigger the event
					continue
				}
				current = new
				ch <- new
			}
		}
	}()

	return ch
}

func (n *networker) Interfaces(iface string, netns string) (pkg.Interfaces, error) {
	getter := func(iface string) ([]netlink.Link, error) {
		if iface != "" {
			l, err := netlink.LinkByName(iface)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to get interface %s", iface)
			}
			return []netlink.Link{l}, nil
		}

		all, err := netlink.LinkList()
		if err != nil {
			return nil, err
		}
		filtered := all[:0]
		for _, l := range all {
			name := l.Attrs().Name

			if name == "lo" ||
				(l.Type() != "device" && name != "zos") {

				continue
			}

			filtered = append(filtered, l)
		}

		return filtered, nil
	}

	interfaces := make(map[string]pkg.Interface)
	f := func(_ ns.NetNS) error {
		links, err := getter(iface)
		if err != nil {
			return errors.Wrapf(err, "failed to get interfaces (query: '%s')", iface)
		}

		for _, link := range links {

			addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
			if err != nil {
				return errors.Wrapf(err, "failed to list addresses of interfaces %s", iface)
			}
			ips := make([]net.IPNet, 0, len(addrs))
			for _, addr := range addrs {
				ip := addr.IP
				if ip6 := ip.To16(); ip6 != nil {
					// ipv6
					if !ip6.IsGlobalUnicast() || ifaceutil.IsULA(ip6) {
						// skip if not global or is ula address
						continue
					}
				}

				ips = append(ips, *addr.IPNet)
			}

			interfaces[link.Attrs().Name] = pkg.Interface{
				Name: link.Attrs().Name,
				Mac:  link.Attrs().HardwareAddr.String(),
				IPs:  ips,
			}
		}

		return nil
	}

	if netns != "" {
		netNS, err := namespace.GetByName(netns)
		if err != nil {
			return pkg.Interfaces{}, errors.Wrapf(err, "failed to get network namespace %s", netns)
		}
		defer netNS.Close()

		if err := netNS.Do(f); err != nil {
			return pkg.Interfaces{}, err
		}
	} else {
		if err := f(nil); err != nil {
			return pkg.Interfaces{}, err
		}
	}

	return pkg.Interfaces{Interfaces: interfaces}, nil
}

func (n *networker) SetPublicConfig(cfg pkg.PublicConfig) error {
	if cfg.Equal(pkg.PublicConfig{}) {
		return fmt.Errorf("public config cannot be unset, only modified")
	}

	current, err := public.LoadPublicConfig()
	if err != nil && err != public.ErrNoPublicConfig {
		return errors.Wrapf(err, "failed to load current public configuration")
	}

	if current != nil && current.Equal(cfg) {
		// nothing to do
		return nil
	}

	if err := public.SavePublicConfig(cfg); err != nil {
		return errors.Wrap(err, "failed to store public config")
	}

	return nil
}

func (n *networker) UnSetPublicConfig() error {
	return public.DeletePublicConfig()
}

func (n *networker) LoadPublicConfig() (pkg.PublicConfig, error) {
	cfg, err := public.LoadPublicConfig()
	if err != nil {
		return pkg.PublicConfig{}, fmt.Errorf("failed to load public config: %w", err)
	}

	if cfg == nil {
		return pkg.PublicConfig{}, fmt.Errorf("public config not found")
	}

	return *cfg, nil
}

func CreateNDMZBridge() (*netlink.Bridge, error) {
	return createNDMZBridge(NDMZBridge, NDMZGw)
}

func createNDMZBridge(name string, gw string) (*netlink.Bridge, error) {
	if !bridge.Exists(name) {
		if _, err := bridge.New(name); err != nil {
			return nil, errors.Wrapf(err, "could not create bridge %s", name)
		}
	}

	if err := options.Set(name, options.IPv6Disable(true)); err != nil {
		return nil, errors.Wrapf(err, "failed to disable ip6 on bridge %s", name)
	}

	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get ndmz bridge: %w", err)
	}

	if link.Type() != "bridge" {
		return nil, fmt.Errorf("ndmz is not a bridge")
	}
	err = netlink.AddrAdd(link, &netlink.Addr{IPNet: NDMZGwIP})
	if err != nil && !os.IsExist(err) {
		return nil, err
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return nil, err
	}

	return link.(*netlink.Bridge), nil
}

func (n *networker) WireguardPorts() ([]uint, error) {
	return n.portSet.List()
}

// GetNet of a network identified by the network ID
func (n *networker) GetNet(id pkg.NetID) (net.IPNet, error) {
	localNR, err := n.networkOf(id)
	if err != nil {
		return net.IPNet{}, errors.Wrapf(err, "couldn't load network (%s)", id.String())
	}

	return localNR.NetworkIPRange.IPNet, nil
}

// GetDefaultGwIP returns the IPs of the default gateways inside the network
// resource identified by the network ID on the local node, for IPv4
func (n *networker) GetDefaultGwIP(id pkg.NetID) (net.IP, error) {
	localNR, err := n.networkOf(id)
	if err != nil {
		return nil, errors.Wrapf(err, "couldn't load network (%s)", id.String())
	}

	// only IP4 atm
	ip := localNR.Subnet.IP.To4()
	if ip == nil {
		return nil, errors.New("nr subnet is not valid IPv4")
	}

	// defaut gw is currently implied to be at `x.x.x.1`
	// also a subnet in a NR is assumed to be a /24
	ip[len(ip)-1] = 1

	return ip, nil
}

func (n *networker) syncWGPorts() error {
	names, err := namespace.List("n")
	if err != nil {
		return err
	}

	readPort := func(name string) (int, error) {
		netNS, err := namespace.GetByName(name)
		if err != nil {
			return 0, err
		}
		defer netNS.Close()

		ifaceName := strings.Replace(name, "n", "w-", 1)

		var port int
		err = netNS.Do(func(_ ns.NetNS) error {
			link, err := wireguard.GetByName(ifaceName)
			if err != nil {
				return err
			}
			d, err := link.Device()
			if err != nil {
				return err
			}

			port = d.ListenPort
			return nil
		})
		if err != nil {
			return 0, err
		}

		return port, nil
	}

	for _, name := range names {
		port, err := readPort(name)
		if err != nil {
			log.Error().Err(err).Str("namespace", name).Msgf("failed to read port for network namespace")
			continue
		}
		// skip error cause we don't care if there are some duplicate at this point
		_ = n.portSet.Add(uint(port))
	}

	return nil
}

func (n *networker) reservePort(port uint16) error {
	log.Debug().Uint16("port", port).Msg("reserve wireguard port")
	err := n.portSet.Add(uint(port))
	if err != nil {
		return errors.Wrap(err, "wireguard listen port already in use, pick another one")
	}

	return nil
}

func (n *networker) releasePort(port uint16) error {
	log.Debug().Uint16("port", port).Msg("release wireguard port")
	n.portSet.Remove(uint(port))
	return nil
}

// setupWireguard configures a Wireguard interface for the network resource
// by checking for existing network configuration and releasing any previously reserved port.
// reserves the specified Wireguard listen port.
// checks if wireguard interface already exists in the namespace, if not it creates the interface in the host namespace.
// If not, it creates a new Wireguard interface in the host namespace and moves it to the network resource
// and configures the Wireguard interface with the private key, listen port, and peers using
// This function handles both initial setup and reconfiguration of existing
// Wireguard interfaces, ensuring proper port management and interface configuration.
func (n networker) setupWireguard(name string, net zos.NetworkLight, netr *resource.Resource) error {
	log.Debug().Msg("setting up wireguard")

	storedNR, err := n.networkOf(pkg.NetID(name))
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to load previous network setup")
	}

	if err == nil {
		if err := n.releasePort(storedNR.WGListenPort); err != nil {
			return err
		}
	}

	if err := n.reservePort(net.WGListenPort); err != nil {
		return err
	}

	wgName, err := netr.WGName()
	if err != nil {
		return errors.Wrap(err, "failed to get wg interface name for network resource")
	}

	exists, err := netr.HasWireguard()
	if err != nil {
		return errors.Wrap(err, "failed to check if network resource has wireguard setup")
	}

	if !exists {
		var wg *wireguard.Wireguard
		wg, err = wireguard.New(wgName)
		if err != nil {
			return errors.Wrapf(err, "failed to create wg interface for network resource '%s'", name)
		}
		if err = netr.SetWireguard(wg); err != nil {
			return errors.Wrap(err, "failed to setup wireguard interface for network resource")
		}
	}

	if len(net.WGPrivateKey) == 0 {
		return n.releasePort(net.WGListenPort)
	}

	if err = netr.ConfigureWG(net.WGPrivateKey); err != nil {
		return errors.Wrap(err, "failed to configure network resource")
	}
	return nil
}

func (n *networker) storeNetwork(name string, wl gridtypes.WorkloadID, network zos.NetworkLight) error {
	// map the network ID to the network namespace
	path := filepath.Join(n.networkDir, name)
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer, err := versioned.NewWriter(file, NetworkSchemaLatestVersion)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(writer)
	if err := enc.Encode(&network); err != nil {
		return err
	}
	link := filepath.Join(n.linkDirPath, wl.String())
	if err := os.Symlink(filepath.Join("../", name), link); err != nil && !os.IsExist(err) {
		return errors.Wrap(err, "failed to create network symlink")
	}
	return nil
}
