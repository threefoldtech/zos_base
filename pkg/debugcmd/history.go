package debugcmd

import (
	"context"
	"encoding/json"

	"github.com/threefoldtech/zos_base/pkg/gridtypes"
)

type HistoryRequest struct {
	Deployment string `json:"deployment"` // Format: "twin-id:contract-id"
}

type WorkloadTransaction struct {
	Seq     int                   `json:"seq"`
	Type    string                `json:"type"`
	Name    string                `json:"name"`
	Created gridtypes.Timestamp   `json:"created"`
	State   gridtypes.ResultState `json:"state"`
	Message string                `json:"message"`
}

type HistoryResponse struct {
	Deployment string                `json:"deployment"`
	History    []WorkloadTransaction `json:"history"`
}

func ParseHistoryRequest(payload []byte) (HistoryRequest, error) {
	var req HistoryRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return req, err
	}
	return req, nil
}

func History(ctx context.Context, deps Deps, req HistoryRequest) (HistoryResponse, error) {
	twinID, contractID, err := ParseDeploymentID(req.Deployment)
	if err != nil {
		return HistoryResponse{}, err
	}

	// TODO: only return history for active deployment.
	history, err := deps.Provision.Changes(ctx, twinID, contractID)
	if err != nil {
		return HistoryResponse{}, err
	}

	transactions := make([]WorkloadTransaction, 0, len(history))
	for idx, wl := range history {
		transactions = append(transactions, WorkloadTransaction{
			Seq:     idx + 1,
			Type:    string(wl.Type),
			Name:    string(wl.Name),
			Created: wl.Result.Created,
			State:   wl.Result.State,
			Message: wl.Result.Error,
		})
	}

	return HistoryResponse{
		Deployment: req.Deployment,
		History:    transactions,
	}, nil
}
