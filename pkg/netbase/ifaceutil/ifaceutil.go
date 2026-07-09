package ifaceutil

import (
	"fmt"
	"net"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/threefoldtech/zos_base/pkg/network/namespace"
	"github.com/vishvananda/netlink"
)

const (
	myceliumPort = "9651"
)

// GetIPsForIFace retrieves the IP addresses for a given interface name in a specified network namespace.
// If the namespace name is empty, it retrieves the IP addresses from the host.
func GetIPsForIFace(iface, nsName string) ([]net.IPNet, error) {
	getIPs := func() ([]net.IPNet, error) {
		var results []net.IPNet

		ln, err := netlink.LinkByName(iface)
		if err != nil {
			return nil, err
		}

		ips, err := netlink.AddrList(ln, netlink.FAMILY_V4)
		if err != nil {
			return nil, err
		}

		for _, ip := range ips {
			if ip.IPNet != nil {
				results = append(results, *ip.IPNet)
			}
		}
		return results, nil
	}

	if nsName == "" {
		return getIPs()
	}

	netns, err := namespace.GetByName(nsName)
	if err != nil {
		return nil, err
	}
	defer netns.Close()

	var results []net.IPNet
	err = netns.Do(func(_ ns.NetNS) error {
		r, e := getIPs()
		results = r
		return e
	})

	return results, err
}

// BuildMyceliumPeerURLs constructs a list of Mycelium peer URLs from a list of IP networks.
func BuildMyceliumPeerURLs(ips []net.IPNet) []string {
	peers := make([]string, len(ips))
	for i, ip := range ips {
		peers[i] = fmt.Sprintf("tcp://%s:%s", ip.IP.String(), myceliumPort)
	}
	return peers
}
