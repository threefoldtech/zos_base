package debugcmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/threefoldtech/zos_base/pkg"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
)

// Storage is the subset of the provision interface used by debug commands.
type Storage interface {
	GetDeployment(ctx context.Context, twin uint32, contractID uint64) (gridtypes.Deployment, error)
	GetDeployments(ctx context.Context, twin uint32) ([]gridtypes.Deployment, error)
	GetTwins(ctx context.Context) ([]uint32, error)
	Changes(ctx context.Context, twin uint32, contractID uint64) ([]gridtypes.Workload, error)
	GetWorkload(ctx context.Context, twin uint32, contractID uint64, name gridtypes.Name) (gridtypes.Workload, bool, error)
}

// VM is the subset of the vmd zbus interface used by debug commands.
type VM interface {
	Exists(ctx context.Context, id string) bool
	Inspect(ctx context.Context, id string) (pkg.VMInfo, error)
	Logs(ctx context.Context, id string) (string, error)
	LogsFull(ctx context.Context, id string) (string, error)
}

// Network is the subset of the network zbus interface used by debug commands.
type Network interface {
	Namespace(ctx context.Context, id zos.NetID) string
}

type Deps struct {
	VM      VM
	Network Network
	Storage Storage
}

// ParseDeploymentID parses a deployment identifier in the format "twin-id:contract-id"
// and returns the twin ID and contract ID.
func ParseDeploymentID(deploymentStr string) (uint32, uint64, error) {
	if deploymentStr == "" {
		return 0, 0, fmt.Errorf("deployment identifier is required")
	}

	parts := strings.Split(deploymentStr, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid deployment format: expected 'twin-id:contract-id', got '%s'", deploymentStr)
	}

	twinID, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid twin ID: %s: %w", parts[0], err)
	}

	contractID, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid contract ID: %s: %w", parts[1], err)
	}

	return uint32(twinID), contractID, nil
}
