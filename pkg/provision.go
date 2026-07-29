package pkg

//go:generate zbusc -module provision -version 0.0.1 -name provision -package stubs github.com/threefoldtech/zos_base/pkg+Provision stubs/provision_stub.go
//go:generate zbusc -module provision -version 0.0.1 -name statistics -package stubs github.com/threefoldtech/zos_base/pkg+Statistics stubs/statistics_stub.go

import (
	"context"

	"github.com/threefoldtech/zos_base/pkg/gridtypes"
)

// Provision interface
type Provision interface {
	DecommissionCached(id string, reason string) error
	// GetWorkloadStatus: returns status, bool(true if workload exits otherwise it is false), error
	GetWorkloadStatus(id string) (gridtypes.ResultState, bool, error)
	CreateOrUpdate(twin uint32, deployment gridtypes.Deployment, update bool) error
	Get(twin uint32, contractID uint64) (gridtypes.Deployment, error)
	List(twin uint32) ([]gridtypes.Deployment, error)
	Changes(twin uint32, contractID uint64) ([]gridtypes.Workload, error)
	ListTwins() ([]uint32, error)
	ListPublicIPs() ([]string, error)
	ListPrivateIPs(twin uint32, network gridtypes.Name) ([]string, error)
	// GetDeployment returns a deployment including soft-deleted ones.
	GetDeployment(twin uint32, contractID uint64) (gridtypes.Deployment, error)
	// GetDeployments returns all deployments for a twin including soft-deleted ones.
	GetDeployments(twin uint32) ([]gridtypes.Deployment, error)
	// GetTwins returns all twins including soft-deleted ones.
	GetTwins() ([]uint32, error)
	// GetWorkload returns the latest workload state by name including soft-deleted ones.
	// Returns (workload, true, nil) if found, (zero, false, nil) if not found.
	GetWorkload(twin uint32, contractID uint64, name gridtypes.Name) (gridtypes.Workload, bool, error)
}

type Statistics interface {
	ReservedStream(ctx context.Context) <-chan gridtypes.Capacity
	Current() (gridtypes.Capacity, error)
	Total() gridtypes.Capacity
	Workloads() (int, error)
	GetCounters() (Counters, error)
	ListGPUs() ([]GPUInfo, error)
}

type Counters struct {
	// Total system capacity
	Total gridtypes.Capacity `json:"total"`
	// Used capacity this include user + system resources
	Used gridtypes.Capacity `json:"used"`
	// System resource reserved by zos
	System gridtypes.Capacity `json:"system"`
	// Users statistics by zos
	Users UsersCounters `json:"users"`
	// OpenConnecions number of open connections in the node
	OpenConnecions int `json:"open_connections"`
}

// UsersCounters the expected counters for deployments and workloads
type UsersCounters struct {
	// Total deployments count
	Deployments int `json:"deployments"`
	// Total workloads count
	Workloads int `json:"workloads"`
	// Last deployment timestamp
	LastDeploymentTimestamp gridtypes.Timestamp `json:"last_deployment_timestamp"`
}

type GPUInfo struct {
	ID       string `json:"id"`
	Vendor   string `json:"vendor"`
	Device   string `json:"device"`
	Vram     uint64 `json:"vram"`
	Contract uint64 `json:"contract"`
}
