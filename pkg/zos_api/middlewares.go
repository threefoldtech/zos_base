package zosapi

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_sdk_go/rmb-sdk-go/peer"

	"github.com/threefoldtech/zos_base/pkg/environment"
)

func (g *ZosAPI) authorized(ctx context.Context, _ []byte) (context.Context, error) {
	user := peer.GetTwinID(ctx)
	if user != g.farmerID {
		return nil, fmt.Errorf("unauthorized")
	}

	return ctx, nil
}

func (g *ZosAPI) adminAuthorized(ctx context.Context, _ []byte) (context.Context, error) {
	user := peer.GetTwinID(ctx)
	cfg, err := environment.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment config: %w", err)
	}

	for _, id := range cfg.AdminTwins {
		if id == user {
			return ctx, nil
		}
	}

	return nil, fmt.Errorf("unauthorized")
}

func (g *ZosAPI) log(ctx context.Context, _ []byte) (context.Context, error) {
	env := peer.GetEnvelope(ctx)
	request := env.GetRequest()
	if request != nil {
		log.Debug().Str("command", request.Command).Msg("received rmb request")
	}
	return ctx, nil
}
