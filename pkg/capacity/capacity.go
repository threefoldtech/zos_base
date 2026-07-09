package capacity

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/host"
	"github.com/threefoldtech/zos_base/pkg"
	"github.com/threefoldtech/zos_base/pkg/capacity/dmi"
	"github.com/threefoldtech/zos_base/pkg/capacity/smartctl"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/kernel"
	"github.com/threefoldtech/zos_base/pkg/storage/filesystem"
	"github.com/threefoldtech/zos_base/pkg/stubs"
)

// Capacity hold the amount of resource unit of a node
type Capacity struct {
	CRU uint64 `json:"cru"`
	MRU uint64 `json:"mru"`
	SRU uint64 `json:"sru"`
	HRU uint64 `json:"hru"`
}

// ResourceOracle is the structure responsible for capacity tracking
type ResourceOracle struct {
	storage *stubs.StorageModuleStub
}

// NewResourceOracle creates a new ResourceOracle
func NewResourceOracle(s *stubs.StorageModuleStub) *ResourceOracle {
	return &ResourceOracle{storage: s}
}

// Total returns the total amount of resource units of the node
func (r *ResourceOracle) Total() (c gridtypes.Capacity, err error) {

	c.CRU, err = r.cru()
	if err != nil {
		return c, err
	}
	c.MRU, err = r.mru()
	if err != nil {
		return c, err
	}
	c.SRU, err = r.sru()
	if err != nil {
		return c, err
	}
	c.HRU, err = r.hru()
	if err != nil {
		return c, err
	}

	return c, nil
}

func IsSecureBoot() (bool, error) {
	// check if node is booted via efi
	const (
		efivars    = "/sys/firmware/efi/efivars"
		secureBoot = "/sys/firmware/efi/efivars/SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c"
	)

	_, err := os.Stat(efivars)
	if os.IsNotExist(err) {
		// not even booted with uefi
		return false, nil
	}

	if !filesystem.IsMountPoint(efivars) {
		if err := syscall.Mount("none", efivars, "efivarfs", 0, ""); err != nil {
			return false, errors.Wrap(err, "failed to mount efivars")
		}

		defer func() {
			if err := syscall.Unmount(efivars, 0); err != nil {
				log.Error().Err(err).Msg("failed to unmount efivars")
			}
		}()
	}

	bytes, err := os.ReadFile(secureBoot)
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, errors.Wrap(err, "failed to read secure boot status")
	}

	if len(bytes) != 5 {
		return false, errors.Wrap(err, "invalid efivar data for secure boot flag")
	}

	return bytes[4] == 1, nil
}

// DMI run and parse dmidecode commands
func (r *ResourceOracle) DMI() (*dmi.DMI, error) {
	return dmi.Decode()
}

// Uptime returns the uptime of the node
func (r *ResourceOracle) Uptime() (uint64, error) {
	info, err := host.Info()
	if err != nil {
		return 0, err
	}
	return info.Uptime, nil
}

// Disks contains the hardware information about the disk of a node
type Disks struct {
	Tool        string          `json:"tool"`
	Environment string          `json:"environment"`
	Aggregator  string          `json:"aggregator"`
	Devices     []smartctl.Info `json:"devices"`
}

// Disks list and parse the hardware information using smartctl
func (r *ResourceOracle) Disks() (d Disks, err error) {
	devices, err := smartctl.ListDevices()
	if errors.Is(err, smartctl.ErrEmpty) {
		// TODO: for now we allow to not have the smartctl dump of all the disks
		log.Warn().Err(err).Msg("smartctl did not found any disk on the system")
		return d, nil
	}
	if err != nil {
		return
	}

	var info smartctl.Info
	d.Devices = make([]smartctl.Info, len(devices))

	for i, device := range devices {
		info, err = smartctl.DeviceInfo(device)
		if err != nil {
			log.Error().Err(err).Msgf("failed to get device info for: %s", device.Path)
			continue
		}
		d.Devices[i] = info
		if d.Environment == "" {
			d.Environment = info.Environment
		}
		if d.Tool == "" {
			d.Tool = info.Tool
		}
	}

	d.Aggregator = "0-OS smartctl aggregator"

	return
}

// GetHypervisor gets the name of the hypervisor used on the node
func (r *ResourceOracle) GetHypervisor() (string, error) {
	out, err := exec.Command("virt-what").CombinedOutput()

	if err != nil {
		return "", errors.Wrap(err, "could not detect if VM or not")
	}

	str := strings.TrimSpace(string(out))
	if len(str) == 0 {
		return "", nil
	}

	lines := strings.Fields(str)
	if len(lines) > 0 {
		return lines[0], nil
	}

	return "", nil
}

// GPUs returns the list of available GPUs as PCI devices
func (r *ResourceOracle) GPUs() ([]PCI, error) {
	if kernel.GetParams().IsGPUDisabled() {
		return []PCI{}, nil
	}
	return ListPCI(GPU)
}

// normalizeBusID converts a bus ID from format "00000000:01:00.0" to "0000:01:00.0"
func normalizeBusID(busID string) string {
	parts := strings.Split(busID, ":")
	if len(parts) != 3 {
		return busID
	}
	domain := strings.TrimLeft(parts[0], "0")
	if domain == "" {
		domain = "0000"
	}
	domain = fmt.Sprintf("%0*s", 4, domain)
	return fmt.Sprintf("%s:%s:%s", domain, parts[1], parts[2])
}

// DisplayNode represents a display device from lshw XML output
type DisplayNode struct {
	Class     string `xml:"class,attr"`
	BusInfo   string `xml:"businfo"`
	Product   string `xml:"product"`
	Vendor    string `xml:"vendor"`
	Resources struct {
		Memory []struct {
			Value string `xml:"value,attr"`
		} `xml:"resource"`
	} `xml:"resources"`
}

// DisplayList represents the root XML structure from lshw
type DisplayList struct {
	Nodes []DisplayNode `xml:"node"`
}

// GetGpuDevice gets the GPU information using lshw command
func GetGpuDevice(p *PCI) (pkg.GPUInfo, error) {
	cmd := exec.Command("lshw", "-C", "display", "-xml")
	output, err := cmd.Output()
	if err != nil {
		return pkg.GPUInfo{}, fmt.Errorf("failed to run lshw command: %w", err)
	}

	var displayList DisplayList
	err = xml.Unmarshal(output, &displayList)
	if err != nil {
		return pkg.GPUInfo{}, fmt.Errorf("failed to parse lshw XML output: %w", err)
	}

	for _, node := range displayList.Nodes {
		if node.Class != "display" {
			continue
		}

		busInfo := node.BusInfo
		if !strings.HasPrefix(busInfo, "pci@") {
			continue
		}

		busID := strings.TrimPrefix(busInfo, "pci@")
		normalizedBusID := normalizeBusID(busID)

		if normalizedBusID != p.Slot {
			continue
		}

		var vram uint64 = 0
		for _, resource := range node.Resources.Memory {
			if strings.Contains(resource.Value, "-") {
				parts := strings.Split(resource.Value, "-")
				if len(parts) == 2 {
					start := strings.TrimSpace(parts[0])
					end := strings.TrimSpace(parts[1])
					if startVal, err1 := strconv.ParseUint(start, 16, 64); err1 == nil {
						if endVal, err2 := strconv.ParseUint(end, 16, 64); err2 == nil {
							size := (endVal - startVal + 1) / (1024 * 1024)
							if size > vram {
								vram = size
							}
						}
					}
				}
			}
		}

		vendor, device, ok := p.GetDevice()
		if !ok {
			return pkg.GPUInfo{}, fmt.Errorf("failed to get vendor and device info")
		}

		return pkg.GPUInfo{
			ID:     p.ShortID(),
			Vendor: vendor.Name,
			Device: device.Name,
			Vram:   vram,
		}, nil
	}

	return pkg.GPUInfo{}, fmt.Errorf("gpu not found in lshw output")
}
