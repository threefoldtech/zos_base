package resource

import (
	"crypto/rand"
	"embed"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/containernetworking/plugins/pkg/ns"
	mapset "github.com/deckarep/golang-set"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	localPkg "github.com/threefoldtech/zos_base/pkg"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
	"github.com/threefoldtech/zos_base/pkg/netbase/nft"
	"github.com/threefoldtech/zos_base/pkg/netbase/wireguard"
	"github.com/threefoldtech/zos_base/pkg/netlight/bridge"
	"github.com/threefoldtech/zos_base/pkg/netlight/ifaceutil"
	"github.com/threefoldtech/zos_base/pkg/netlight/namespace"
	"github.com/threefoldtech/zos_base/pkg/netlight/options"
	"github.com/threefoldtech/zos_base/pkg/netlight/tuntap"
	"github.com/threefoldtech/zos_base/pkg/zinit"
	"github.com/vishvananda/netlink"
)

const (
	infPublic   = "public"
	infPrivate  = "private"
	infMycelium = "mycelium"
)

//go:embed nft/rules.nft
var nftRules embed.FS

type Resource struct {
	name string

	// local network resources
	resource zos.NetworkLight
	// network IP range, usually a /16
	networkIPRange net.IPNet
}

// Create creates a network resource (please check docs)
// name: is the name of the network resource. The Create function is idempotent which means if the same name is used the function
// will not recreate the resource.
// master: Normally the br-ndmz bridge, this is the resource "way out" to the public internet. A `public` interface is created and wired
// to the master bridge
// ndmzIP: the IP assigned to the `public` interface.
// ndmzGwIP: the gw Ip for the resource. Normally this is the ip assigned to the master bridge.
// privateNet: optional private network range
// seed: mycelium seed
func Create(name string, master *netlink.Bridge, ndmzIP *net.IPNet, ndmzGwIP *net.IPNet, privateNet *net.IPNet, nr zos.NetworkLight) (*Resource, error) {
	privateNetBr := fmt.Sprintf("r%s", name)
	myBr := fmt.Sprintf("m%s", name)
	nsName := fmt.Sprintf("n%s", name)
	peerPrefix := name
	if len(name) > 4 {
		peerPrefix = name[0:4]
	}
	bridges := []string{myBr}
	if privateNet != nil {
		bridges = append(bridges, privateNetBr)
	}

	for _, name := range bridges {
		if !bridge.Exists(name) {
			if _, err := bridge.New(name); err != nil {
				return nil, fmt.Errorf("could not create bridge %s: %w", name, err)
			}
		}
	}

	netNS, err := namespace.GetByName(nsName)
	if err != nil {
		netNS, err = namespace.Create(nsName)
		if err != nil {
			return nil, fmt.Errorf("failed to create namespace %s", err)
		}
	}

	defer netNS.Close()

	if privateNet != nil {
		if privateNet.IP.To4() == nil {
			return nil, fmt.Errorf("private ip range must be IPv4 address")
		}

		if !ifaceutil.Exists(infPrivate, netNS) {
			privateLink, err := ifaceutil.MakeVethPair(infPrivate, privateNetBr, 1500, peerPrefix)
			if err != nil {
				return nil, fmt.Errorf("failed to create private link: %w", err)
			}
			err = netlink.LinkSetNsFd(privateLink, int(netNS.Fd()))
			if err != nil {
				return nil, fmt.Errorf("failed to move public link %s to namespace:%s : %w", infPublic, netNS.Path(), err)
			}

		}
	}

	// create public interface and attach it to ndmz bridge
	if !ifaceutil.Exists(infPublic, netNS) {
		pubLink, err := ifaceutil.MakeVethPair(infPublic, master.Name, 1500, peerPrefix)
		if err != nil {
			return nil, fmt.Errorf("failed to create public link in namespace %s: %w", nsName, err)
		}
		err = netlink.LinkSetNsFd(pubLink, int(netNS.Fd()))
		if err != nil {
			return nil, fmt.Errorf("failed to move public link %s to namespace:%s : %w", infPublic, netNS.Path(), err)
		}

	}

	if !ifaceutil.Exists(infMycelium, netNS) {
		myceliumLink, err := ifaceutil.MakeVethPair(infMycelium, myBr, 1500, peerPrefix)
		if err != nil {
			return nil, fmt.Errorf("failed to create mycelium link: %w", err)
		}
		err = netlink.LinkSetNsFd(myceliumLink, int(netNS.Fd()))
		if err != nil {
			return nil, fmt.Errorf("failed to move mycelium link: %s to namespace:%s : %w", infMycelium, netNS.Path(), err)
		}
	}

	err = netNS.Do(func(_ ns.NetNS) error {
		if err := ifaceutil.SetLoUp(); err != nil {
			return fmt.Errorf("failed to set lo up for namespace '%s': %w", nsName, err)
		}

		if err := setLinkAddr(infPublic, ndmzIP); err != nil {
			return fmt.Errorf("couldn't set link addr for public interface in namespace %s: %w", nsName, err)
		}
		if err := options.SetIPv6Forwarding(true); err != nil {
			return fmt.Errorf("failed to enable ipv6 forwarding in namespace %q: %w", nsName, err)
		}

		if privateNet != nil {
			privGwIp := privateNet.IP.To4()
			// this is to take the first IP of the private range
			// and use it as the gw address for the entire private range
			privGwIp[net.IPv4len-1] = 1
			// this IP is then set on the private interface
			privateNet.IP = privGwIp
			if err := setLinkAddr(infPrivate, privateNet); err != nil {
				return fmt.Errorf("couldn't set link addr for private interface in namespace %s: %w", nsName, err)
			}
		}

		if err := netlink.RouteAdd(&netlink.Route{
			Gw: ndmzGwIP.IP,
		}); err != nil && !os.IsExist(err) {
			return fmt.Errorf("failed to add ndmz routing")
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	rules, err := nftRules.Open("nft/rules.nft")
	if err != nil {
		return nil, fmt.Errorf("failed to load nft.rules file")
	}

	if err := nft.Apply(rules, nsName); err != nil {
		return nil, fmt.Errorf("failed to apply nft rules for namespace '%s': %w", name, err)
	}
	rules.Close()
	return &Resource{name, nr, nr.NetworkIPRange.IPNet}, SetupMycelium(netNS, infMycelium, nr.Mycelium.Key)
}

func Delete(name string) error {
	var errs error
	nsName := fmt.Sprintf("n%s", name)
	netNS, err := namespace.GetByName(nsName)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	if err := destroyMycelium(netNS, zinit.Default()); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("failed to destroy mycelium: %w", err))
	}

	if err := namespace.Delete(netNS); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("failed to delete namespace: %w", err))
	}

	privateNetBr := fmt.Sprintf("r%s", name)
	myBr := fmt.Sprintf("m%s", name)

	if err := bridge.Delete(privateNetBr); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("failed to delete private bridge: %w", err))
	}

	if err := bridge.Delete(myBr); err != nil {
		errs = multierror.Append(errs, fmt.Errorf("failed to delete mycelium bridge: %w", err))
	}

	return errs

}

func setLinkAddr(name string, ip *net.IPNet) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("failed to set link address: %w", err)
	}

	// if err := options.Set(name, options.IPv6Disable(false)); err != nil {
	// 	return fmt.Errorf("failed to enable ip6 on interface %s: %w", name, err)
	// }

	addr := netlink.Addr{
		IPNet: ip,
	}
	err = netlink.AddrAdd(link, &addr)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to add ip address to link: %w", err)
	}

	return netlink.LinkSetUp(link)
}

// Get return resource handler
func Get(name string) (*Resource, error) {
	nsName := fmt.Sprintf("n%s", name)

	if namespace.Exists(nsName) {
		return &Resource{name: name}, nil
	}

	return nil, fmt.Errorf("resource not found: %s", name)
}

var defaultNet = net.IPNet{
	IP:   net.ParseIP("0.0.0.0"),
	Mask: net.IPv4Mask(0, 0, 0, 0),
}

func (r *Resource) AttachPrivate(id string, vmIp net.IP) (device localPkg.TapDevice, err error) {
	nsName, err := r.Namespace()
	if err != nil {
		return
	}

	netNs, err := namespace.GetByName(nsName)
	if err != nil {
		return
	}
	if vmIp[3] == 1 {
		return device, fmt.Errorf("ip %s is reserved", vmIp.String())
	}

	var addrs []netlink.Addr

	if err = netNs.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(infPrivate)
		if err != nil {
			return err
		}
		addrs, err = netlink.AddrList(link, netlink.FAMILY_V4)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return
	}
	if len(addrs) != 1 {
		return device, fmt.Errorf("expect addresses on private interface to be 1 got %d", len(addrs))
	}
	gw := addrs[0].IPNet
	if !gw.Contains(vmIp) {
		return device, fmt.Errorf("ip not in range")
	}

	deviceName := ifaceutil.DeviceNameFromInputBytes([]byte(id))
	tapName := fmt.Sprintf("b-%s", deviceName)

	privateNetBr := fmt.Sprintf("r%s", r.name)
	hw := ifaceutil.HardwareAddrFromInputBytes([]byte(tapName))

	ip := &net.IPNet{
		IP:   vmIp,
		Mask: gw.Mask,
	}
	_, getLinkErr := netlink.LinkByName(tapName)
	if getLinkErr != nil {
		mtap, err := tuntap.CreateTap(tapName, privateNetBr)
		if err != nil {
			return localPkg.TapDevice{}, err
		}

		if err = netlink.AddrAdd(mtap, &netlink.Addr{
			IPNet: ip,
		}); err != nil {
			return localPkg.TapDevice{}, err
		}
	}

	routes := []localPkg.Route{
		{
			Net:     defaultNet,
			Gateway: gw.IP,
		},
	}
	return localPkg.TapDevice{
		Name:   tapName,
		Mac:    hw,
		IP:     ip,
		Routes: routes,
	}, nil
}

func (r *Resource) AttachMycelium(id string, seed []byte) (device localPkg.TapDevice, err error) {
	nsName, err := r.Namespace()
	if err != nil {
		return
	}

	netNS, err := namespace.GetByName(nsName)
	if err != nil {
		return
	}
	name := filepath.Base(netNS.Path())
	netSeed, err := os.ReadFile(filepath.Join(MyceliumSeedDir, name))
	if err != nil {
		return
	}

	inspect, err := InspectMycelium(netSeed)
	if err != nil {
		return
	}

	ip, gw, err := inspect.IPFor(seed)
	if err != nil {
		return
	}

	deviceName := ifaceutil.DeviceNameFromInputBytes([]byte(id))
	tapName := fmt.Sprintf("m-%s", deviceName)

	myBr := fmt.Sprintf("m%s", r.name)
	hw := ifaceutil.HardwareAddrFromInputBytes([]byte(tapName))

	_, err = tuntap.CreateTap(tapName, myBr)
	if err != nil {
		return
	}

	route := localPkg.Route{
		Net: net.IPNet{
			IP:   net.ParseIP("400::"),
			Mask: net.CIDRMask(7, 128),
		},
		Gateway: gw.IP,
	}
	return localPkg.TapDevice{
		Name:   tapName,
		IP:     &ip,
		Routes: []localPkg.Route{route},
		Mac:    hw,
	}, nil
}

func (r *Resource) AttachMyceliumZDB(id string, zdbNS ns.NetNS) (err error) {
	nsName, err := r.Namespace()
	if err != nil {
		return
	}

	netNS, err := namespace.GetByName(nsName)
	if err != nil {
		return
	}
	name := filepath.Base(netNS.Path())
	netSeed, err := os.ReadFile(filepath.Join(MyceliumSeedDir, name))
	if err != nil {
		return
	}

	inspect, err := InspectMycelium(netSeed)
	if err != nil {
		return
	}
	seed := make([]byte, 6)

	_, err = rand.Read(seed)
	if err != nil {
		return
	}

	ip, gw, err := inspect.IPFor(seed)
	if err != nil {
		return
	}

	deviceName := ifaceutil.DeviceNameFromInputBytes([]byte(id))
	linkName := fmt.Sprintf("m-%s", deviceName)

	peerPrefix := r.name
	if len(r.name) > 4 {
		peerPrefix = name[0:4]
	}

	if !ifaceutil.Exists(linkName, zdbNS) {
		zdbLink, err := ifaceutil.MakeVethPair(linkName, HostMyceliumBr, 1500, peerPrefix)
		if err != nil {
			return fmt.Errorf("failed to create zdb link %s : %w", linkName, err)
		}
		err = netlink.LinkSetNsFd(zdbLink, int(zdbNS.Fd()))
		if err != nil {
			return fmt.Errorf("failed to move zdb link: %s to namespace:%s : %w", linkName, netNS.Path(), err)
		}

		return zdbNS.Do(func(_ ns.NetNS) error {
			err = setLinkAddr(linkName, &ip)
			if err != nil {
				return err
			}

			if err := ifaceutil.SetLoUp(); err != nil {
				return fmt.Errorf("failed to set lo up for namespace '%s': %w", nsName, err)
			}

			return netlink.RouteAdd(&netlink.Route{
				Dst: &net.IPNet{
					IP:   net.ParseIP("400::"),
					Mask: net.CIDRMask(7, 128),
				},
				Gw: gw.IP,
			})
		})
	}
	return nil
	//
}

func (r *Resource) Seed() (seed []byte, err error) {
	nsName, err := r.Namespace()
	if err != nil {
		return
	}

	netNS, err := namespace.GetByName(nsName)
	if err != nil {
		return
	}
	name := filepath.Base(netNS.Path())
	seed, err = os.ReadFile(filepath.Join(MyceliumSeedDir, name))
	return
}

// WGName returns the name of the wireguard interface to create for the network resource
func (r *Resource) WGName() (string, error) {
	wgName := fmt.Sprintf("w-%s", r.name)
	if len(wgName) > 15 {
		return "", errors.Errorf("network namespace too long %s", wgName)
	}
	return wgName, nil
}

// HasWireguard checks if network resource has wireguard setup up
func (r *Resource) HasWireguard() (bool, error) {
	nsName, err := r.Namespace()
	if err != nil {
		return false, err
	}

	nrNetNS, err := namespace.GetByName(nsName)
	if err != nil {
		return false, err
	}

	defer nrNetNS.Close()

	wgName, err := r.WGName()
	if err != nil {
		return false, err
	}

	exist := false
	err = nrNetNS.Do(func(_ ns.NetNS) error {
		_, err = wireguard.GetByName(wgName)

		if errors.As(err, &netlink.LinkNotFoundError{}) {
			return nil
		} else if err != nil {
			return err
		}

		exist = true
		return nil
	})

	return exist, err
}

// Namespace returns the name of the network namespace to create for the network resource
func (r *Resource) Namespace() (string, error) {
	name := fmt.Sprintf("n%s", r.name)
	if len(name) > 15 {
		return "", errors.Errorf("network namespace too long %s", name)
	}
	return name, nil
}

// SetWireguard sets wireguard of this network resource
func (r *Resource) SetWireguard(wg *wireguard.Wireguard) error {
	nsName, err := r.Namespace()
	if err != nil {
		return err
	}

	nrNetNS, err := namespace.GetByName(nsName)
	if err != nil {
		return err
	}
	defer nrNetNS.Close()

	return netlink.LinkSetNsFd(wg, int(nrNetNS.Fd()))
}

// ConfigureWG sets the routes and IP addresses on the
// wireguard interface of the network resources
func (r *Resource) ConfigureWG(privateKey string) error {
	wgPeers, err := r.wgPeers()
	if err != nil {
		return errors.Wrap(err, "failed to wireguard peer configuration")
	}

	nsName, err := r.Namespace()
	if err != nil {
		return err
	}
	netNS, err := namespace.GetByName(nsName)
	if err != nil {
		return fmt.Errorf("network namespace %s does not exits", nsName)
	}

	handler := func(_ ns.NetNS) error {
		wgName, err := r.WGName()
		if err != nil {
			return err
		}

		wg, err := wireguard.GetByName(wgName)
		if err != nil {
			return errors.Wrapf(err, "failed to get wireguard interface %s", wgName)
		}

		if err = wg.Configure(privateKey, int(r.resource.WGListenPort), wgPeers); err != nil {
			return errors.Wrap(err, "failed to configure wireguard interface")
		}

		addrs, err := netlink.AddrList(wg, netlink.FAMILY_ALL)
		if err != nil {
			return err
		}
		curAddrs := mapset.NewSet()
		for _, addr := range addrs {
			curAddrs.Add(addr.IPNet.String())
		}

		newAddrs := mapset.NewSet()
		newAddrs.Add(wgIP(&r.resource.Subnet.IPNet).String())

		toRemove := curAddrs.Difference(newAddrs)
		toAdd := newAddrs.Difference(curAddrs)

		log.Info().Msgf("current %s", curAddrs.String())
		log.Info().Msgf("to add %s", toAdd.String())
		log.Info().Msgf("to remove %s", toRemove.String())

		for addr := range toAdd.Iter() {
			addr, _ := addr.(string)
			log.Debug().Str("ip", addr).Msg("set ip on wireguard interface")
			if err := wg.SetAddr(addr); err != nil && !os.IsExist(err) {
				return errors.Wrapf(err, "failed to set address %s on wireguard interface %s", addr, wg.Attrs().Name)
			}
		}

		for addr := range toRemove.Iter() {
			addr, _ := addr.(string)
			log.Debug().Str("ip", addr).Msg("unset ip on wireguard interface")
			if err := wg.UnsetAddr(addr); err != nil && !os.IsNotExist(err) {
				return errors.Wrapf(err, "failed to unset address %s on wireguard interface %s", addr, wg.Attrs().Name)
			}
		}

		route := &netlink.Route{
			LinkIndex: wg.Attrs().Index,
			Dst:       &r.networkIPRange,
		}
		if err := netlink.RouteAdd(route); err != nil && !os.IsExist(err) {
			log.Error().
				Err(err).
				Str("route", route.String()).
				Msg("fail to set route")
			return errors.Wrapf(err, "failed to add route %s", route.String())
		}

		return nil
	}

	return netNS.Do(handler)
}

func (nr *Resource) wgPeers() ([]*wireguard.Peer, error) {
	wgPeers := make([]*wireguard.Peer, 0, len(nr.resource.Peers)+1)

	for _, peer := range nr.resource.Peers {

		allowedIPs := make([]string, 0, len(peer.AllowedIPs))
		for _, ip := range peer.AllowedIPs {
			allowedIPs = append(allowedIPs, ip.String())
		}

		wgPeer := &wireguard.Peer{
			PublicKey:  peer.WGPublicKey,
			AllowedIPs: allowedIPs,
			Endpoint:   peer.Endpoint,
		}

		log.Info().Str("peer prefix", peer.Subnet.String()).Msg("generate wireguard configuration for peer")
		wgPeers = append(wgPeers, wgPeer)
	}

	return wgPeers, nil
}

func wgIP(subnet *net.IPNet) *net.IPNet {
	// example: 10.3.1.0 -> 100.64.3.1
	a := subnet.IP[len(subnet.IP)-3]
	b := subnet.IP[len(subnet.IP)-2]

	return &net.IPNet{
		IP:   net.IPv4(0x64, 0x40, a, b),
		Mask: net.CIDRMask(16, 32),
	}
}
