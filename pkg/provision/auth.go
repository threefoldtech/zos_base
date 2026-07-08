package provision

import (
	"context"
	"fmt"
	"time"

	cache "github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg/stubs"
)

const (
	keyExpiration = 60 * time.Minute
	keyCleanup    = 10 * time.Minute
)

type substrateTwins struct {
	substrateGateway *stubs.SubstrateGatewayStub
	mem              *cache.Cache
}

// NewSubstrateTwins creates a substrate users db that implements the provision.Users interface.
func NewSubstrateTwins(substrateGateway *stubs.SubstrateGatewayStub) (Twins, error) {
	return &substrateTwins{
		substrateGateway: substrateGateway,
		mem:              cache.New(keyExpiration, keyCleanup),
	}, nil
}

// GetKey gets twins public key
func (s *substrateTwins) GetKey(id uint32) ([]byte, error) {
	cacheKey := fmt.Sprint(id)
	if value, ok := s.mem.Get(cacheKey); ok {
		return value.([]byte), nil
	}

	log.Debug().Uint32("twin", id).Msg("twin public key cache expired, fetching from substrate")
	user, err := s.substrateGateway.GetTwin(context.Background(), id)
	if err != nil {
		return nil, errors.Wrapf(err, "could not get user with id '%d'", id)
	}

	pk := user.Account.PublicKey()
	s.mem.Set(cacheKey, pk, cache.DefaultExpiration)
	return pk, nil
}

type substrateAdmins struct {
	substrateGateway *stubs.SubstrateGatewayStub
	twin             uint32
	mem              *cache.Cache
}

// NewSubstrateAdmins creates a substrate twins db that implements the provision.Users interface.
// but it also make sure the user is an admin
func NewSubstrateAdmins(substrateGateway *stubs.SubstrateGatewayStub, farmID uint32) (Twins, error) {
	farm, err := substrateGateway.GetFarm(context.Background(), farmID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get farm")
	}

	return &substrateAdmins{
		substrateGateway: substrateGateway,
		twin:             uint32(farm.TwinID),
		mem:              cache.New(keyExpiration, keyCleanup),
	}, nil
}

// GetKey gets twin public key if twin is valid admin
func (s *substrateAdmins) GetKey(id uint32) ([]byte, error) {
	if id != s.twin {
		return nil, fmt.Errorf("twin with id '%d' is not an admin", id)
	}

	cacheKey := fmt.Sprint(id)
	if value, ok := s.mem.Get(cacheKey); ok {
		return value.([]byte), nil
	}

	log.Debug().Uint32("twin", id).Msg("admin public key cache expired, fetching from substrate")
	twin, err := s.substrateGateway.GetTwin(context.Background(), id)
	if err != nil {
		return nil, errors.Wrapf(err, "could not get admin twin with id '%d'", id)
	}

	pk := twin.Account.PublicKey()
	s.mem.Set(cacheKey, pk, cache.DefaultExpiration)
	return pk, nil
}
