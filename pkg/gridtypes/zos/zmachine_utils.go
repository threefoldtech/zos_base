package zos

import (
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/threefoldtech/zos_base/pkg/gridtypes"
)

const (
	MyceliumIPSeedLen = 6
)

// MachineInterface structure
type MachineInterface struct {
	// Network name (znet name) to join
	Network gridtypes.Name `json:"network"`
	// IP of the zmachine on this network must be a valid Ip in the
	// selected network
	IP net.IP `json:"ip"`
}

type MyceliumIP struct {
	// Network name (znet name) to join
	Network gridtypes.Name
	// Seed is a six bytes random number that is used
	// as a seed to derive a vm mycelium IP.
	//
	// This means that a VM "ip" can be moved to another VM if needed
	// by simply using the same seed.
	// This of course will only work if the network mycelium setup is using
	// the same HexKey
	Seed Bytes `json:"hex_seed"`
}

func (c *MyceliumIP) Challenge(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "%s", c.Network); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "%x", c.Seed); err != nil {
		return err
	}

	return nil
}

// MachineCapacity structure
type MachineCapacity struct {
	CPU    uint8          `json:"cpu"`
	Memory gridtypes.Unit `json:"memory"`
}

func (c *MachineCapacity) String() string {
	return fmt.Sprintf("cpu(%d)+mem(%d)", c.CPU, c.Memory)
}

// Challenge builder
func (c *MachineCapacity) Challenge(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "%d", c.CPU); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "%d", c.Memory); err != nil {
		return err
	}

	return nil
}

// MachineMount structure
type MachineMount struct {
	// Name is name of a zmount. The name must be a valid zmount
	// in the same deployment as the zmachine
	Name gridtypes.Name `json:"name"`
	// Mountpoint inside the container. Not used if the zmachine
	// is running in a vm mode.
	Mountpoint string `json:"mountpoint"`
}

// Challenge builder
func (m *MachineMount) Challenge(w io.Writer) error {
	if _, err := fmt.Fprintf(w, "%s", m.Name); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "%s", m.Mountpoint); err != nil {
		return err
	}

	return nil
}

// GPU ID
// Used by a VM, a GPU id is in the format <slot>/<vendor>/<device>
// This can be queried either from the node features on the chain
// or listed via the node rmb API.
// example of a valid gpu definition `0000:28:00.0/1002/731f“
type GPU string

func (g GPU) Parts() (slot, vendor, device string, err error) {
	parts := strings.Split(string(g), "/")
	if len(parts) != 3 {
		err = fmt.Errorf("invalid GPU id format '%s'", g)
		return
	}

	return parts[0], parts[1], parts[2], nil
}
