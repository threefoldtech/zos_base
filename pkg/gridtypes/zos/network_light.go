package zos

import (
	"fmt"
	"io"

	gridtypes "github.com/threefoldtech/zos_base/pkg/gridtypes"
)

// NetworkLight is the description of a part of a network local to a specific node.
// A network workload defines a wireguard network that is usually spans multiple nodes. One of the nodes must work as an access node
// in other words, it must be reachable from other nodes, hence it needs to have a `PublicConfig`.
// Since the user library creates all deployments upfront then all wireguard keys, and ports must be pre-deterministic and must be
// also created upfront.
// A network structure basically must consist of
// - The network information (IP range) must be an ipv4 /16 range
// - The local (node) peer definition (subnet of the network ip range, wireguard secure key, wireguard port if any)
// - List of other peers that are part of the same network with their own config
// - For each PC or a laptop (for each wireguard peer) there must be a peer in the peer list (on all nodes)
// This is why this can get complicated.
type NetworkLight struct {
	// IPV4 subnet for this network resource
	// this must be a valid subnet of the entire network ip range.
	// for example 10.1.1.0/24
	Subnet gridtypes.IPNet `json:"subnet"`

	// Optional mycelium configuration. If provided
	// VMs in this network can use the mycelium feature.
	// if no mycelium configuration is provided, vms can't
	// get mycelium IPs.
	Mycelium Mycelium `json:"mycelium,omitempty"`

	// wg config
	// IP range of the network, must be an IPv4 /16
	// for example a 10.1.0.0/16
	NetworkIPRange gridtypes.IPNet `json:"ip_range"`

	// The private wg key of this node (this peer) which is installing this
	// network workload right now.
	// This has to be filled in by the user (and not generated for example)
	// because other peers need to be installed as well (with this peer public key)
	// hence it's easier to configure everything one time at the user side and then
	// apply everything on all nodes at once
	WGPrivateKey string `json:"wireguard_private_key"`

	// WGListenPort is the wireguard listen port on this node. this has
	// to be filled in by the user for same reason as private key (other nodes need to know about it)
	// To find a free port you have to ask the node first by a call over RMB about which ports are possible
	// to use.
	WGListenPort uint16 `json:"wireguard_listen_port"`

	// Peers is a list of other peers in this network
	Peers []Peer `json:"peers"`
}

// Valid checks if the network resource is valid.
func (n NetworkLight) Valid(getter gridtypes.WorkloadGetter) error {
	if len(n.Subnet.IP) == 0 {
		return fmt.Errorf("network resource subnet cannot empty")
	}

	if err := n.Mycelium.Valid(); err != nil {
		return err
	}

	return nil
}

// Challenge implements WorkloadData
func (n NetworkLight) Challenge(b io.Writer) error {
	if _, err := fmt.Fprintf(b, "%s", n.NetworkIPRange.String()); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(b, "%s", n.Subnet.String()); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(b, "%s", n.WGPrivateKey); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(b, "%d", n.WGListenPort); err != nil {
		return err
	}

	for _, p := range n.Peers {
		if err := p.Challenge(b); err != nil {
			return err
		}
	}

	if err := n.Mycelium.Challenge(b); err != nil {
		return err
	}

	return nil
}

// Capacity implementation
func (n NetworkLight) Capacity() (gridtypes.Capacity, error) {
	return gridtypes.Capacity{}, nil
}
