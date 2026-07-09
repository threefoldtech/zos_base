package checks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/vm"
)

const vmdVolatileDir = "/var/run/cache/vmd"

type VMChecker struct {
	workloadID gridtypes.WorkloadID
	vmID       string
	cfgPath    string
	machine    *vm.Machine
	vmExists   func(ctx context.Context, id string) bool
}

func (vc *VMChecker) Name() string { return "vm" }

func (vc *VMChecker) Run(ctx context.Context, data *CheckData) []HealthCheck {
	workloadID, err := gridtypes.NewWorkloadID(data.Twin, data.Contract, data.Workload.Name)
	if err != nil {
		return []HealthCheck{failure("vm.init", fmt.Sprintf("invalid workload ID: %v", err), nil)}
	}

	vc.workloadID = workloadID
	vc.vmID = workloadID.String()
	vc.cfgPath = filepath.Join(vmdVolatileDir, workloadID.String())
	vc.vmExists = data.VM

	return []HealthCheck{
		vc.checkConfig(),
		vc.checkVMD(ctx),
		vc.checkProcess(),
		vc.checkDisks(),
		vc.checkVirtioFS(),
	}
}

func (vc *VMChecker) loadMachine() (*vm.Machine, error) {
	if vc.machine != nil {
		return vc.machine, nil
	}
	machine, err := vm.MachineFromFile(vc.cfgPath)
	if err != nil {
		return nil, err
	}
	vc.machine = machine
	return machine, nil
}

func (vc *VMChecker) checkConfig() HealthCheck {
	if _, err := os.Stat(vc.cfgPath); err != nil {
		return failure("vm.config", fmt.Sprintf("config file not found: %v", err), map[string]interface{}{"path": vc.cfgPath})
	}
	if _, err := vm.MachineFromFile(vc.cfgPath); err != nil {
		return failure("vm.config", fmt.Sprintf("config file invalid: %v", err), map[string]interface{}{"path": vc.cfgPath})
	}
	return success("vm.config", "config valid", map[string]interface{}{"path": vc.cfgPath, "vm_id": vc.vmID})
}

func (vc *VMChecker) checkVMD(ctx context.Context) HealthCheck {
	if !vc.vmExists(ctx, vc.vmID) {
		return failure("vm.vmd", "vmd reports VM does not exist", map[string]interface{}{"vm_id": vc.vmID})
	}
	return success("vm.vmd", "vmd reports VM exists", map[string]interface{}{"vm_id": vc.vmID})
}

func (vc *VMChecker) checkProcess() HealthCheck {
	ps, err := vm.Find(vc.vmID)
	if err != nil {
		return failure("vm.process", fmt.Sprintf("process not found: %v", err), map[string]interface{}{"vm_id": vc.vmID})
	}
	return success("vm.process", "process running", map[string]interface{}{"vm_id": vc.vmID, "pid": ps.Pid})
}

func (vc *VMChecker) checkDisks() HealthCheck {
	machine, err := vc.loadMachine()
	if err != nil {
		return failure("vm.disks", "config not available", map[string]interface{}{"vm_id": vc.vmID})
	}

	for _, disk := range machine.Disks {
		if disk.Path == "" {
			continue
		}
		if _, err := os.Stat(disk.Path); err != nil {
			return failure("vm.disks", fmt.Sprintf("disk missing: %s", disk.Path), map[string]interface{}{"path": disk.Path, "vm_id": vc.vmID})
		}
	}

	// TODO: check for files on disks?

	return success("vm.disks", "all disks valid", map[string]interface{}{"vm_id": vc.vmID})
}

func (vc *VMChecker) checkVirtioFS() HealthCheck {
	machine, err := vc.loadMachine()
	if err != nil {
		return failure("vm.virtiofs", fmt.Sprintf("config unavailable: %v", err), map[string]interface{}{"vm_id": vc.vmID})
	}

	for i := range machine.FS {
		sock := filepath.Join("/var/run", fmt.Sprintf("virtio-%s-%d.socket", vc.vmID, i))
		if _, err := os.Stat(sock); err != nil {
			return failure("vm.virtiofs", fmt.Sprintf("socket missing: %s", sock), map[string]interface{}{"socket": sock, "vm_id": vc.vmID})
		}
	}

	return success("vm.virtiofs", "all virtiofs sockets present", map[string]interface{}{"vm_id": vc.vmID})
}

// TODO: add cloud-console check

var VMCheckerInstance = &VMChecker{}
