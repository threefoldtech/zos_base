package debugcmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
)

type InfoRequest struct {
	Deployment string `json:"deployment"` // Format: "twin-id:contract-id"
	Workload   string `json:"workload"`   // Workload name
	Verbose    bool   `json:"verbose"`    // If true, return full logs
}

type InfoResponse struct {
	WorkloadID string      `json:"workload_id"`
	Type       string      `json:"type"`
	Name       string      `json:"name"`
	Info       interface{} `json:"info,omitempty"`
	Logs       string      `json:"logs,omitempty"`
}

func ParseInfoRequest(payload []byte) (InfoRequest, error) {
	var req InfoRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return req, err
	}
	return req, nil
}

func Info(ctx context.Context, deps Deps, req InfoRequest) (InfoResponse, error) {
	if req.Workload == "" {
		return InfoResponse{}, fmt.Errorf("workload name is required")
	}

	twinID, contractID, err := ParseDeploymentID(req.Deployment)
	if err != nil {
		return InfoResponse{}, err
	}

	deployment, err := deps.Provision.Get(ctx, twinID, contractID)
	if err != nil {
		return InfoResponse{}, fmt.Errorf("failed to get deployment: %w", err)
	}

	var workload *gridtypes.Workload
	for i := range deployment.Workloads {
		if string(deployment.Workloads[i].Name) == req.Workload {
			workload = &deployment.Workloads[i]
			break
		}
	}

	if workload == nil {
		return InfoResponse{}, fmt.Errorf("workload '%s' not found in deployment", req.Workload)
	}

	workloadID, _ := gridtypes.NewWorkloadID(twinID, contractID, workload.Name)
	resp := InfoResponse{
		WorkloadID: workloadID.String(),
		Type:       string(workload.Type),
		Name:       string(workload.Name),
	}

	// TODO: Handle different workload types
	switch workload.Type {
	case zos.ZMachineType, zos.ZMachineLightType:
		return handleZMachineInfo(ctx, deps, workloadID.String(), req.Verbose, resp)
	case zos.NetworkType, zos.NetworkLightType:
		return handleNetworkInfo(ctx, deps, twinID, workload, resp)
	default:
		return InfoResponse{}, fmt.Errorf("workload type '%s' not supported for info command", workload.Type)
	}
}

func handleZMachineInfo(ctx context.Context, deps Deps, vmID string, verbose bool, resp InfoResponse) (InfoResponse, error) {
	// TODO: extend inspect to view more info of the vm
	info, err := deps.VM.Inspect(ctx, vmID)
	if err != nil {
		return InfoResponse{}, fmt.Errorf("failed to inspect vm: %w", err)
	}
	resp.Info = info

	var raw string
	if verbose {
		raw, err = deps.VM.LogsFull(ctx, vmID)
	} else {
		raw, err = deps.VM.Logs(ctx, vmID)
	}
	if err != nil {
		return InfoResponse{}, fmt.Errorf("failed to get vm logs: %w", err)
	}

	resp.Logs = raw
	return resp, nil
}

func handleNetworkInfo(ctx context.Context, deps Deps, twinID uint32, workload *gridtypes.Workload, resp InfoResponse) (InfoResponse, error) {
	netID := zos.NetworkID(twinID, workload.Name)
	nsName := deps.Network.Namespace(ctx, netID)

	networkInfo := map[string]interface{}{
		"net_id":    netID.String(),
		"namespace": nsName,
		"state":     string(workload.Result.State),
	}

	resp.Info = networkInfo
	resp.Logs = "Network workloads don't support logs"
	return resp, nil
}
