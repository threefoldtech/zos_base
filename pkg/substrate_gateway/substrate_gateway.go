package substrategw

import (
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v3"
	"github.com/centrifuge/go-substrate-rpc-client/v4/types"
	"github.com/rs/zerolog/log"
	substrate "github.com/threefoldtech/tfchain/clients/tfchain-client-go"
	"github.com/threefoldtech/zos_base/pkg"
)

type substrateGateway struct {
	sub      *substrate.Substrate
	mu       sync.Mutex
	identity substrate.Identity
}

func NewSubstrateGateway(manager substrate.Manager, identity substrate.Identity) (pkg.SubstrateGateway, error) {
	sub, err := manager.Substrate()
	if err != nil {
		return nil, err
	}
	gw := &substrateGateway{
		sub:      sub,
		mu:       sync.Mutex{},
		identity: identity,
	}
	return gw, nil
}

// UpdateSubstrateGatewayConnection allow modules to update substrate manager so that the node can recover chain outage
func (g *substrateGateway) UpdateSubstrateGatewayConnection(manager substrate.Manager) error {
	sub, err := manager.Substrate()
	if err != nil {
		return err
	}

	// close the old connection
	g.sub.Close()

	g.sub = sub
	return nil
}

// createBackoff creates an exponential backoff configuration for substrate calls
func createBackoff() backoff.BackOff {
	exp := backoff.NewExponentialBackOff()
	exp.MaxInterval = 2 * time.Second
	exp.InitialInterval = 500 * time.Millisecond
	exp.MaxElapsedTime = 5 * time.Second
	return exp
}

func (g *substrateGateway) GetZosVersion() (string, error) {
	log.Debug().Str("method", "GetZosVersion").Msg("method called")

	var result string
	err := backoff.Retry(func() error {
		version, err := g.sub.GetZosVersion()
		if err != nil {
			log.Debug().Err(err).Msg("GetZosVersion failed, retrying")
			return err
		}
		result = version
		return nil
	}, createBackoff())

	return result, err
}

func (g *substrateGateway) CreateNode(node substrate.Node) (uint32, error) {
	log.Debug().
		Str("method", "CreateNode").
		Uint32("twin id", uint32(node.TwinID)).
		Uint32("farm id", uint32(node.FarmID)).
		Msg("method called")

	var result uint32
	g.mu.Lock()
	defer g.mu.Unlock()
	err := backoff.Retry(func() error {
		nodeID, err := g.sub.CreateNode(g.identity, node)
		if err != nil {
			log.Debug().Err(err).Msg("CreateNode failed, retrying")
			return err
		}
		result = nodeID
		return nil
	}, createBackoff())

	return result, err
}

func (g *substrateGateway) CreateTwin(relay string, pk []byte) (uint32, error) {
	log.Debug().Str("method", "CreateTwin").Str("relay", relay).Str("pk", hex.EncodeToString(pk)).Msg("method called")

	var result uint32
	g.mu.Lock()
	defer g.mu.Unlock()
	err := backoff.Retry(func() error {
		twinID, err := g.sub.CreateTwin(g.identity, relay, pk)
		if err != nil {
			log.Debug().Err(err).Msg("CreateTwin failed, retrying")
			return err
		}
		result = twinID
		return nil
	}, createBackoff())

	return result, err
}

func (g *substrateGateway) EnsureAccount(activationURL []string, termsAndConditionsLink string, termsAndConditionsHash string) (info substrate.AccountInfo, err error) {
	log.Debug().
		Str("method", "EnsureAccount").
		Strs("activation url", activationURL).
		Str("terms and conditions link", termsAndConditionsLink).
		Str("terms and conditions hash", termsAndConditionsHash).
		Msg("method called")

	g.mu.Lock()
	defer g.mu.Unlock()
	for _, url := range activationURL {
		err = backoff.Retry(func() error {
			accountInfo, retryErr := g.sub.EnsureAccount(g.identity, url, termsAndConditionsLink, termsAndConditionsHash)
			if retryErr != nil {
				log.Debug().Str("activation url", url).Err(retryErr).Msg("EnsureAccount failed, retrying")
				return retryErr
			}
			info = accountInfo
			return nil
		}, createBackoff())

		// check other activationURL only if EnsureAccount failed with ActivationServiceError
		if err == nil || !errors.As(err, &substrate.ActivationServiceError{}) {
			return
		}
		log.Debug().Str("activation url", url).Err(err).Msg("failed to EnsureAccount with ActivationServiceError")
	}
	return
}

func (g *substrateGateway) GetContract(id uint64) (result substrate.Contract, serr pkg.SubstrateError) {
	log.Trace().Str("method", "GetContract").Uint64("id", id).Msg("method called")

	err := backoff.Retry(func() error {
		contract, retryErr := g.sub.GetContract(id)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint64("id", id).Msg("GetContract failed, retrying")
			return retryErr
		}
		result = *contract
		return nil
	}, createBackoff())

	serr = buildSubstrateError(err)
	return
}

func (g *substrateGateway) GetContractIDByNameRegistration(name string) (result uint64, serr pkg.SubstrateError) {
	log.Trace().Str("method", "GetContractIDByNameRegistration").Str("name", name).Msg("method called")

	err := backoff.Retry(func() error {
		contractID, retryErr := g.sub.GetContractIDByNameRegistration(name)
		if retryErr != nil {
			log.Debug().Err(retryErr).Str("name", name).Msg("GetContractIDByNameRegistration failed, retrying")
			return retryErr
		}
		result = contractID
		return nil
	}, createBackoff())

	serr = buildSubstrateError(err)
	return
}

func (g *substrateGateway) GetFarm(id uint32) (result substrate.Farm, err error) {
	log.Trace().Str("method", "GetFarm").Uint32("id", id).Msg("method called")

	err = backoff.Retry(func() error {
		farm, retryErr := g.sub.GetFarm(id)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint32("id", id).Msg("GetFarm failed, retrying")
			return retryErr
		}
		result = *farm
		return nil
	}, createBackoff())

	return
}

func (g *substrateGateway) GetNode(id uint32) (result substrate.Node, err error) {
	log.Trace().Str("method", "GetNode").Uint32("id", id).Msg("method called")

	err = backoff.Retry(func() error {
		node, retryErr := g.sub.GetNode(id)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint32("id", id).Msg("GetNode failed, retrying")
			return retryErr
		}
		result = *node
		return nil
	}, createBackoff())

	return
}

func (g *substrateGateway) GetNodeByTwinID(twin uint32) (result uint32, serr pkg.SubstrateError) {
	log.Trace().Str("method", "GetNodeByTwinID").Uint32("twin", twin).Msg("method called")

	err := backoff.Retry(func() error {
		nodeID, retryErr := g.sub.GetNodeByTwinID(twin)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint32("twin", twin).Msg("GetNodeByTwinID failed, retrying")
			return retryErr
		}
		result = nodeID
		return nil
	}, createBackoff())

	serr = buildSubstrateError(err)
	return
}

func (g *substrateGateway) GetNodeContracts(node uint32) ([]types.U64, error) {
	log.Trace().Str("method", "GetNodeContracts").Uint32("node", node).Msg("method called")

	var result []types.U64
	err := backoff.Retry(func() error {
		contracts, retryErr := g.sub.GetNodeContracts(node)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint32("node", node).Msg("GetNodeContracts failed, retrying")
			return retryErr
		}
		result = contracts
		return nil
	}, createBackoff())

	return result, err
}

func (g *substrateGateway) GetNodeRentContract(node uint32) (result uint64, serr pkg.SubstrateError) {
	log.Trace().Str("method", "GetNodeRentContract").Uint32("node", node).Msg("method called")

	err := backoff.Retry(func() error {
		contractID, retryErr := g.sub.GetNodeRentContract(node)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint32("node", node).Msg("GetNodeRentContract failed, retrying")
			return retryErr
		}
		result = contractID
		return nil
	}, createBackoff())

	serr = buildSubstrateError(err)
	return
}

func (g *substrateGateway) GetNodes(farmID uint32) ([]uint32, error) {
	log.Trace().Str("method", "GetNodes").Uint32("farm id", farmID).Msg("method called")

	var result []uint32
	err := backoff.Retry(func() error {
		nodes, retryErr := g.sub.GetNodes(farmID)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint32("farm id", farmID).Msg("GetNodes failed, retrying")
			return retryErr
		}
		result = nodes
		return nil
	}, createBackoff())

	return result, err
}

func (g *substrateGateway) GetPowerTarget(nodeID uint32) (power substrate.NodePower, err error) {
	log.Trace().Str("method", "GetPowerTarget").Uint32("node id", nodeID).Msg("method called")

	err = backoff.Retry(func() error {
		nodePower, retryErr := g.sub.GetPowerTarget(nodeID)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint32("node id", nodeID).Msg("GetPowerTarget failed, retrying")
			return retryErr
		}
		power = nodePower
		return nil
	}, createBackoff())

	return
}

func (g *substrateGateway) GetTwin(id uint32) (result substrate.Twin, err error) {
	log.Trace().Str("method", "GetTwin").Uint32("id", id).Msg("method called")

	err = backoff.Retry(func() error {
		twin, retryErr := g.sub.GetTwin(id)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint32("id", id).Msg("GetTwin failed, retrying")
			return retryErr
		}
		result = *twin
		return nil
	}, createBackoff())

	return
}

func (g *substrateGateway) GetTwinByPubKey(pk []byte) (result uint32, serr pkg.SubstrateError) {
	log.Trace().Str("method", "GetTwinByPubKey").Str("pk", hex.EncodeToString(pk)).Msg("method called")

	err := backoff.Retry(func() error {
		twinID, retryErr := g.sub.GetTwinByPubKey(pk)
		if retryErr != nil {
			log.Debug().Err(retryErr).Str("pk", hex.EncodeToString(pk)).Msg("GetTwinByPubKey failed, retrying")
			return retryErr
		}
		result = twinID
		return nil
	}, createBackoff())

	serr = buildSubstrateError(err)
	return
}

func (g *substrateGateway) Report(consumptions []substrate.NruConsumption) (types.Hash, error) {
	contractIDs := make([]uint64, 0, len(consumptions))
	for _, v := range consumptions {
		contractIDs = append(contractIDs, uint64(v.ContractID))
	}
	log.Debug().Str("method", "Report").Uints64("contract ids", contractIDs).Msg("method called")

	var result types.Hash
	g.mu.Lock()
	defer g.mu.Unlock()
	err := backoff.Retry(func() error {
		hash, retryErr := g.sub.Report(g.identity, consumptions)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uints64("contract ids", contractIDs).Msg("Report failed, retrying")
			return retryErr
		}
		result = hash
		return nil
	}, createBackoff())

	return result, err
}

func (g *substrateGateway) SetContractConsumption(resources ...substrate.ContractResources) error {
	contractIDs := make([]uint64, 0, len(resources))
	for _, v := range resources {
		contractIDs = append(contractIDs, uint64(v.ContractID))
	}
	log.Debug().Str("method", "SetContractConsumption").Uints64("contract ids", contractIDs).Msg("method called")

	g.mu.Lock()
	defer g.mu.Unlock()
	err := backoff.Retry(func() error {
		retryErr := g.sub.SetContractConsumption(g.identity, resources...)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uints64("contract ids", contractIDs).Msg("SetContractConsumption failed, retrying")
			return retryErr
		}
		return nil
	}, createBackoff())

	return err
}

func (g *substrateGateway) SetNodePowerState(up bool) (hash types.Hash, err error) {
	log.Debug().Str("method", "SetNodePowerState").Bool("up", up).Msg("method called")

	g.mu.Lock()
	defer g.mu.Unlock()
	err = backoff.Retry(func() error {
		resultHash, retryErr := g.sub.SetNodePowerState(g.identity, up)
		if retryErr != nil {
			log.Debug().Err(retryErr).Bool("up", up).Msg("SetNodePowerState failed, retrying")
			return retryErr
		}
		hash = resultHash
		return nil
	}, createBackoff())

	return
}

func (g *substrateGateway) UpdateNode(node substrate.Node) (uint32, error) {
	log.Debug().Str("method", "UpdateNode").Msg("method called")

	var result uint32
	g.mu.Lock()
	defer g.mu.Unlock()
	err := backoff.Retry(func() error {
		nodeID, retryErr := g.sub.UpdateNode(g.identity, node)
		if retryErr != nil {
			log.Debug().Err(retryErr).Msg("UpdateNode failed, retrying")
			return retryErr
		}
		result = nodeID
		return nil
	}, createBackoff())

	return result, err
}

func (g *substrateGateway) UpdateNodeUptimeV2(uptime uint64, timestampHint uint64) (hash types.Hash, err error) {
	log.Debug().
		Str("method", "UpdateNodeUptimeV2").
		Uint64("uptime", uptime).
		Uint64("timestamp hint", timestampHint).
		Msg("submitting uptime extrinsic")

	g.mu.Lock()
	defer g.mu.Unlock()
	err = backoff.Retry(func() error {
		resultHash, retryErr := g.sub.UpdateNodeUptimeV2(g.identity, uptime, timestampHint)
		if retryErr != nil {
			log.Debug().Err(retryErr).Uint64("uptime", uptime).Uint64("timestamp hint", timestampHint).Msg("UpdateNodeUptimeV2 failed, retrying")
			return retryErr
		}
		hash = resultHash
		return nil
	}, createBackoff())

	log.Debug().
		Str("method", "UpdateNodeUptimeV2").
		Uint64("uptime", uptime).
		Uint64("timestamp hint", timestampHint).
		Msg("uptime extrinsic submitted successfully")

	return
}

func (g *substrateGateway) GetTime() (time.Time, error) {
	log.Trace().Str("method", "Time").Msg("method called")

	var result time.Time
	err := backoff.Retry(func() error {
		timeResult, retryErr := g.sub.Time()
		if retryErr != nil {
			log.Debug().Err(retryErr).Msg("GetTime failed, retrying")
			return retryErr
		}
		result = timeResult
		return nil
	}, createBackoff())

	return result, err
}

func buildSubstrateError(err error) (serr pkg.SubstrateError) {
	if err == nil {
		return
	}

	serr.Err = err
	serr.Code = pkg.CodeGenericError

	if errors.Is(err, substrate.ErrNotFound) {
		serr.Code = pkg.CodeNotFound
	} else if errors.Is(err, substrate.ErrBurnTransactionNotFound) {
		serr.Code = pkg.CodeBurnTransactionNotFound
	} else if errors.Is(err, substrate.ErrRefundTransactionNotFound) {
		serr.Code = pkg.CodeRefundTransactionNotFound
	} else if errors.Is(err, substrate.ErrFailedToDecode) {
		serr.Code = pkg.CodeFailedToDecode
	} else if errors.Is(err, substrate.ErrInvalidVersion) {
		serr.Code = pkg.CodeInvalidVersion
	} else if errors.Is(err, substrate.ErrUnknownVersion) {
		serr.Code = pkg.CodeUnknownVersion
	} else if errors.Is(err, substrate.ErrIsUsurped) {
		serr.Code = pkg.CodeIsUsurped
	} else if errors.Is(err, substrate.ErrAccountNotFound) {
		serr.Code = pkg.CodeAccountNotFound
	} else if errors.Is(err, substrate.ErrDepositFeeNotFound) {
		serr.Code = pkg.CodeDepositFeeNotFound
	} else if errors.Is(err, substrate.ErrMintTransactionNotFound) {
		serr.Code = pkg.CodeMintTransactionNotFound
	}
	return
}
