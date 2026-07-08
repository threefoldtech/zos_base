package checks

import (
	"context"

	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
)

type Checker interface {
	Name() string
	Run(ctx context.Context, data *CheckData) []HealthCheck
}

type HealthCheck struct {
	Name     string                 `json:"name"`
	OK       bool                   `json:"ok"`
	Message  string                 `json:"message,omitempty"`
	Evidence map[string]interface{} `json:"evidence,omitempty"`
}

type CheckData struct {
	Twin     uint32
	Contract uint64
	Workload gridtypes.Workload
	VM       func(ctx context.Context, id string) bool
	Network  func(ctx context.Context, id zos.NetID) string
}

func success(name, message string, evidence map[string]interface{}) HealthCheck {
	if evidence == nil {
		evidence = make(map[string]interface{})
	}
	return HealthCheck{Name: name, OK: true, Message: message, Evidence: evidence}
}

func failure(name, message string, evidence map[string]interface{}) HealthCheck {
	if evidence == nil {
		evidence = make(map[string]interface{})
	}
	return HealthCheck{Name: name, OK: false, Message: message, Evidence: evidence}
}

func IsHealthy(checks []HealthCheck) bool {
	for _, check := range checks {
		if !check.OK {
			return false
		}
	}
	return true
}

func Run(ctx context.Context, workloadType gridtypes.WorkloadType, data *CheckData) []HealthCheck {
	switch workloadType {
	case zos.NetworkType, zos.NetworkLightType:
		return NetworkCheckerInstance.Run(ctx, data)
	case zos.ZMachineType, zos.ZMachineLightType:
		return VMCheckerInstance.Run(ctx, data)
	default:
		return nil
	}
}
