package debugcmd

import (
	"context"
	"encoding/json"

	"github.com/threefoldtech/zos_base/pkg/gridtypes"
)

type GetRequest struct {
	Deployment string `json:"deployment"` // Format: "twin-id:contract-id"
}

type GetResponse struct {
	Deployment gridtypes.Deployment `json:"deployment"`
}

func ParseGetRequest(payload []byte) (GetRequest, error) {
	var req GetRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return req, err
	}
	return req, nil
}

func Get(ctx context.Context, deps Deps, req GetRequest) (GetResponse, error) {
	twinID, contractID, err := ParseDeploymentID(req.Deployment)
	if err != nil {
		return GetResponse{}, err
	}

	// TODO: only return active deployment. should return all
	deployment, err := deps.Provision.Get(ctx, twinID, contractID)
	if err != nil {
		return GetResponse{}, err
	}

	return GetResponse{Deployment: deployment}, nil
}
