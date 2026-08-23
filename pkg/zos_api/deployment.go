package zosapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/gridtypes/zos"
	"github.com/threefoldtech/zos_sdk_go/rmb-sdk-go/peer"
)

// rootfsVolumePrefix is how the VM primitive names a container VM's writable
// rootfs subvolume (see pkg/primitives/vm/utils.go). We reuse it here to
// transfer/restore the user's rootfs changes.
const rootfsVolumePrefix = "rootfs:"

const (
	waitWorkloadTimeout  = 3 * time.Minute
	waitWorkloadInterval = 2 * time.Second
)

// transferItem maps a workload in the source deployment to a presigned URL.
type transferItem struct {
	WorkloadName gridtypes.Name `json:"workload_name"`
	URL          string         `json:"url"`
	// Size is the volume size to (re)create on the target for a rootfs volume
	// download; ignored for uploads and for zmount downloads.
	Size gridtypes.Unit `json:"size,omitempty"`
}

func (g *ZosAPI) deploymentDeployHandler(ctx context.Context, payload []byte) (interface{}, error) {
	var deployment gridtypes.Deployment
	if err := json.Unmarshal(payload, &deployment); err != nil {
		return nil, err
	}
	err := g.provisionStub.CreateOrUpdate(ctx, peer.GetTwinID(ctx), deployment, false)
	return nil, err
}

func (g *ZosAPI) deploymentUpdateHandler(ctx context.Context, payload []byte) (interface{}, error) {
	var deployment gridtypes.Deployment
	if err := json.Unmarshal(payload, &deployment); err != nil {
		return nil, err
	}
	err := g.provisionStub.CreateOrUpdate(ctx, peer.GetTwinID(ctx), deployment, true)
	return nil, err
}

func (g *ZosAPI) deploymentDeleteHandler(ctx context.Context, payload []byte) (interface{}, error) {
	return nil, fmt.Errorf("deletion over the api is disabled, please cancel your contract instead")
}

func (g *ZosAPI) deploymentGetHandler(ctx context.Context, payload []byte) (interface{}, error) {
	var args struct {
		ContractID uint64 `json:"contract_id"`
	}
	err := json.Unmarshal(payload, &args)
	if err != nil {
		return nil, err
	}

	return g.provisionStub.Get(ctx, peer.GetTwinID(ctx), args.ContractID)

}

func (g *ZosAPI) deploymentListHandler(ctx context.Context, payload []byte) (interface{}, error) {
	return g.provisionStub.List(ctx, peer.GetTwinID(ctx))
}

func (g *ZosAPI) deploymentChangesHandler(ctx context.Context, payload []byte) (interface{}, error) {
	var args struct {
		ContractID uint64 `json:"contract_id"`
	}
	err := json.Unmarshal(payload, &args)
	if err != nil {
		return nil, err
	}
	return g.provisionStub.Changes(ctx, peer.GetTwinID(ctx), args.ContractID)
}

// deploymentTransferHandler moves the persistent bytes of a deployment (zmount
// disks and/or the VM rootfs writable layer) to another node. It pauses the
// source deployment for a consistent copy, then uploads each requested workload
// to a caller-provided presigned S3 URL (HTTP PUT). Used on the OLD node during
// a contract move.
func (g *ZosAPI) deploymentTransferHandler(ctx context.Context, payload []byte) (interface{}, error) {
	var args struct {
		ContractID uint64         `json:"contract_id"`
		Uploads    []transferItem `json:"uploads"`
	}
	if err := json.Unmarshal(payload, &args); err != nil {
		return nil, err
	}

	twin := peer.GetTwinID(ctx)
	deployment, err := g.provisionStub.Get(ctx, twin, args.ContractID)
	if err != nil {
		return nil, err
	}

	// resolve + validate the requested workloads up front so obvious errors are
	// returned synchronously, before we detach the long-running uploads
	jobs, err := resolveTransferJobs(&deployment, args.Uploads)
	if err != nil {
		return nil, err
	}

	// freeze the source VM(s) so disks/rootfs are quiescent while copied
	if err := g.provisionStub.PauseDeployment(ctx, twin, args.ContractID); err != nil {
		return nil, fmt.Errorf("failed to pause deployment before transfer: %w", err)
	}

	// uploads take minutes — far longer than the RMB response window — so run them
	// in the background (detached from the request ctx) and report progress via
	// logs. Completion is observable by the objects appearing at the target URLs.
	go g.runUploads(args.ContractID, jobs)

	return map[string]interface{}{"status": "started", "uploads": len(jobs)}, nil
}

// deploymentPrepareHandler stages a deployment on this node without starting the
// zmachine: it provisions the network + zmount(s) (and skips the VM), then pulls
// each requested workload's bytes from a caller-provided presigned S3 URL (HTTP
// GET) into the freshly created zmount disk / rootfs volume. Used on the NEW node
// during a contract move; the VM is started by a follow-up call.
func (g *ZosAPI) deploymentPrepareHandler(ctx context.Context, payload []byte) (interface{}, error) {
	var args struct {
		Deployment gridtypes.Deployment `json:"deployment"`
		Downloads  []transferItem       `json:"downloads"`
		// Start boots the zmachine automatically once all downloads finish.
		Start bool `json:"start"`
	}
	if err := json.Unmarshal(payload, &args); err != nil {
		return nil, err
	}

	twin := peer.GetTwinID(ctx)

	// resolve + validate the requested workloads up front (synchronous errors)
	jobs, err := resolveTransferJobs(&args.Deployment, args.Downloads)
	if err != nil {
		return nil, err
	}

	// provision network + zmount(s), skip the zmachine (async)
	if err := g.provisionStub.PrepareDeployment(ctx, twin, args.Deployment); err != nil {
		return nil, err
	}

	// downloads take minutes — run them in the background. If Start is set, the VM
	// is booted automatically once "all downloads complete"; the caller can then
	// wait for the zmachine workload to reach the Ok state.
	go g.runDownloads(twin, args.Deployment.ContractID, jobs, args.Start)

	return map[string]interface{}{"status": "started", "downloads": len(jobs)}, nil
}

// deploymentStartHandler boots the zmachine(s) of a deployment previously staged
// via prepare. Used on the NEW node to finish a contract move once the migrated
// data (zmount disks, rootfs) is in place.
func (g *ZosAPI) deploymentStartHandler(ctx context.Context, payload []byte) (interface{}, error) {
	var args struct {
		ContractID uint64 `json:"contract_id"`
	}
	if err := json.Unmarshal(payload, &args); err != nil {
		return nil, err
	}
	return nil, g.provisionStub.StartDeployment(ctx, peer.GetTwinID(ctx), args.ContractID)
}

// waitWorkloadProvisioned blocks until the workload with the given global id
// reaches an OK state, or returns an error on failure/timeout.
func (g *ZosAPI) waitWorkloadProvisioned(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, waitWorkloadTimeout)
	defer cancel()

	ticker := time.NewTicker(waitWorkloadInterval)
	defer ticker.Stop()

	for {
		state, exists, err := g.provisionStub.GetWorkloadStatus(ctx, id)
		if err == nil && exists {
			if state.IsOkay() {
				return nil
			}
			if state == gridtypes.StateError {
				return fmt.Errorf("workload %q failed to provision", id)
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for workload %q to be provisioned", id)
		case <-ticker.C:
		}
	}
}

// transferJob is a resolved upload/download unit: a workload's global id + target URL.
type transferJob struct {
	name gridtypes.Name
	typ  gridtypes.WorkloadType
	id   string
	url  string
	size gridtypes.Unit
}

// resolveTransferJobs maps requested items to workloads in the deployment and
// rejects anything that isn't a zmount or a zmachine.
func resolveTransferJobs(dl *gridtypes.Deployment, items []transferItem) ([]transferJob, error) {
	jobs := make([]transferJob, 0, len(items))
	for _, it := range items {
		wl, err := dl.Get(it.WorkloadName)
		if err != nil {
			return nil, err
		}
		switch wl.Type {
		case zos.ZMountType, zos.ZMachineType, zos.ZMachineLightType:
		default:
			return nil, fmt.Errorf("workload %q of type %q is not transferable", it.WorkloadName, wl.Type)
		}
		jobs = append(jobs, transferJob{
			name: it.WorkloadName,
			typ:  wl.Type,
			id:   wl.ID.String(),
			url:  it.URL,
			size: it.Size,
		})
	}
	return jobs, nil
}

// runUploads streams each job's bytes to its URL (zmount raw disk, or zmachine
// rootfs). Runs detached from any request context; progress is logged.
func (g *ZosAPI) runUploads(contractID uint64, jobs []transferJob) {
	ctx := context.Background()
	for _, j := range jobs {
		var err error
		switch j.typ {
		case zos.ZMountType:
			err = g.storageStub.DiskUpload(ctx, j.id, j.url)
		default: // zmachine rootfs
			err = g.storageStub.VolumeUpload(ctx, rootfsVolumePrefix+j.id, j.url)
		}
		if err != nil {
			log.Error().Err(err).Uint64("contract", contractID).Str("workload", string(j.name)).
				Msg("deployment transfer: upload failed")
			return
		}
		log.Info().Uint64("contract", contractID).Str("workload", string(j.name)).
			Msg("deployment transfer: upload complete")
	}
	log.Info().Uint64("contract", contractID).Msg("deployment transfer: all uploads complete")
}

// runDownloads pulls each job's bytes from its URL into the freshly staged
// zmount disk / rootfs volume. Runs detached; progress is logged. The VM must be
// started only after "all downloads complete".
func (g *ZosAPI) runDownloads(twin uint32, contractID uint64, jobs []transferJob, start bool) {
	ctx := context.Background()
	for _, j := range jobs {
		var err error
		switch j.typ {
		case zos.ZMountType:
			// the disk is created by the async prepare; wait for it before writing
			if err = g.waitWorkloadProvisioned(ctx, j.id); err == nil {
				err = g.storageStub.DiskDownload(ctx, j.id, j.url)
			}
		default: // zmachine rootfs; VolumeDownload creates the volume
			err = g.storageStub.VolumeDownload(ctx, rootfsVolumePrefix+j.id, j.size, j.url)
		}
		if err != nil {
			log.Error().Err(err).Uint64("contract", contractID).Str("workload", string(j.name)).
				Msg("deployment prepare: download failed")
			return
		}
		log.Info().Uint64("contract", contractID).Str("workload", string(j.name)).
			Msg("deployment prepare: download complete")
	}
	log.Info().Uint64("contract", contractID).Msg("deployment prepare: all downloads complete")

	// boot the VM now that its data is in place (the caller waits for the zmachine
	// workload to reach Ok)
	if start {
		if err := g.provisionStub.StartDeployment(ctx, twin, contractID); err != nil {
			log.Error().Err(err).Uint64("contract", contractID).Msg("deployment prepare: auto-start failed")
		} else {
			log.Info().Uint64("contract", contractID).Msg("deployment prepare: auto-start scheduled")
		}
	}
}
