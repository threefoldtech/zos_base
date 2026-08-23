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
	// PrepareDeployment stages a deployment (provisions all but the zmachine) for a contract move
	PrepareDeployment(twin uint32, deployment gridtypes.Deployment) error
	// PauseDeployment pauses a deployment (used to freeze a source VM before transferring its data)
	PauseDeployment(twin uint32, id uint64) error
	// StartDeployment boots the zmachine(s) of a deployment previously staged via PrepareDeployment
	StartDeployment(twin uint32, id uint64) error
	Get(twin uint32, contractID uint64) (gridtypes.Deployment, error)
	List(twin uint32) ([]gridtypes.Deployment, error)
	Changes(twin uint32, contractID uint64) ([]gridtypes.Workload, error)
	ListTwins() ([]uint32, error)
	ListPublicIPs() ([]string, error)
	ListPrivateIPs(twin uint32, network gridtypes.Name) ([]string, error)
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
