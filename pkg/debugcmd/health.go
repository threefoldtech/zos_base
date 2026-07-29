package debugcmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/threefoldtech/zos_base/pkg/debugcmd/checks"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
)

type HealthRequest struct {
	Deployment string                 `json:"deployment"`
	Options    map[string]interface{} `json:"options,omitempty"`
}

type HealthStatus string

const (
	HealthHealthy   HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
)

type WorkloadHealth struct {
	WorkloadID string               `json:"workload_id"`
	Type       string               `json:"type"`
	Name       string               `json:"name"`
	Status     HealthStatus         `json:"status"`
	Checks     []checks.HealthCheck `json:"checks"`
}

type HealthResponse struct {
	TwinID     uint32           `json:"twin_id"`
	ContractID uint64           `json:"contract_id"`
	Workloads  []WorkloadHealth `json:"workloads"`
}

func ParseHealthRequest(payload []byte) (HealthRequest, error) {
	var req HealthRequest
	return req, json.Unmarshal(payload, &req)
}

func Health(ctx context.Context, deps Deps, req HealthRequest) (HealthResponse, error) {
	var twinID uint32
	var contractID uint64
	var err error

	hasSystemProbe := req.Options != nil && req.Options["system_probe"] != nil

	if req.Deployment != "" {
		twinID, contractID, err = ParseDeploymentID(req.Deployment)
		if err != nil {
			return HealthResponse{}, err
		}
	} else if !hasSystemProbe {
		return HealthResponse{}, fmt.Errorf("deployment is required when system_probe is not specified")
	}

	out := HealthResponse{TwinID: twinID, ContractID: contractID}

	if hasSystemProbe {
		if probeCmd, ok := req.Options["system_probe"].(string); ok && probeCmd != "" {
			checkData := &checks.CheckData{Twin: twinID, Contract: contractID}
			allChecks := checks.NewSystemChecker(probeCmd).Run(ctx, checkData)
			if len(allChecks) > 0 {
				out.Workloads = append(out.Workloads, newWorkloadHealth("system", "diagnostic", "system.probe", allChecks))
			}
		}
	}

	if req.Deployment != "" {
		deployment, err := deps.Storage.GetDeployment(ctx, twinID, contractID)
		if err != nil {
			return HealthResponse{}, fmt.Errorf("failed to get deployment: %w", err)
		}

		for _, wl := range deployment.Workloads {
			workloadID, err := gridtypes.NewWorkloadID(twinID, contractID, wl.Name)
			if err != nil {
				continue
			}

			checkData := &checks.CheckData{
				Network:  deps.Network.Namespace,
				VM:       deps.VM.Exists,
				Twin:     twinID,
				Contract: contractID,
				Workload: wl,
			}

			allChecks := checks.Run(ctx, wl.Type, checkData)
			if len(allChecks) > 0 {
				out.Workloads = append(out.Workloads, newWorkloadHealth(
					workloadID.String(),
					string(wl.Type),
					string(wl.Name),
					allChecks,
				))
			}
		}
	}

	return out, nil
}

func newWorkloadHealth(workloadID, workloadType, name string, allChecks []checks.HealthCheck) WorkloadHealth {
	status := HealthUnhealthy
	if checks.IsHealthy(allChecks) {
		status = HealthHealthy
	}
	return WorkloadHealth{
		WorkloadID: workloadID,
		Type:       workloadType,
		Name:       name,
		Status:     status,
		Checks:     allChecks,
	}
}
