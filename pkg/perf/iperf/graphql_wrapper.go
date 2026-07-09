package iperf

import (
	"context"

	"github.com/threefoldtech/zos_base/pkg/perf/graphql"
)

// GraphQLClient interface for mocking GraphQL operations
type GraphQLClient interface {
	GetUpNodes(ctx context.Context, nodesNum int, farmID, excludeFarmID uint32, ipv4, ipv6 bool) ([]graphql.Node, error)
}
