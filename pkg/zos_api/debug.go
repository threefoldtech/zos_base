package zosapi

import (
	"context"

	"github.com/threefoldtech/zos_base/pkg/debugcmd"
)

func (g *ZosAPI) debugDeploymentListHandler(ctx context.Context, payload []byte) (interface{}, error) {
	req, err := debugcmd.ParseListRequest(payload)
	if err != nil {
		return nil, err
	}
	return debugcmd.List(ctx, g.debugDeps(), req)
}

func (g *ZosAPI) debugDeploymentGetHandler(ctx context.Context, payload []byte) (interface{}, error) {
	req, err := debugcmd.ParseGetRequest(payload)
	if err != nil {
		return nil, err
	}
	return debugcmd.Get(ctx, g.debugDeps(), req)
}

func (g *ZosAPI) debugDeploymentHistoryHandler(ctx context.Context, payload []byte) (interface{}, error) {
	req, err := debugcmd.ParseHistoryRequest(payload)
	if err != nil {
		return nil, err
	}
	return debugcmd.History(ctx, g.debugDeps(), req)
}

func (g *ZosAPI) debugDeploymentInfoHandler(ctx context.Context, payload []byte) (interface{}, error) {
	req, err := debugcmd.ParseInfoRequest(payload)
	if err != nil {
		return nil, err
	}
	return debugcmd.Info(ctx, g.debugDeps(), req)
}

func (g *ZosAPI) debugDeploymentHealthHandler(ctx context.Context, payload []byte) (interface{}, error) {
	req, err := debugcmd.ParseHealthRequest(payload)
	if err != nil {
		return nil, err
	}
	return debugcmd.Health(ctx, g.debugDeps(), req)
}

func (g *ZosAPI) debugDeps() debugcmd.Deps {
	return debugcmd.Deps{
		VM:      g.vmStub,
		Network: g.networkerStub,
		Storage: g.provisionStub,
	}
}
