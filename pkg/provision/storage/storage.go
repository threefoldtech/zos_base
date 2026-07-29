package storage

import (
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/provision"
	bolt "go.etcd.io/bbolt"
)

var (
	ErrTransactionNotExist = fmt.Errorf("no transaction found")
	ErrInvalidWorkloadType = fmt.Errorf("invalid workload type")
)

const (
	keyVersion              = "version"
	keyMetadata             = "metadata"
	keyDescription          = "description"
	keySignatureRequirement = "signature_requirement"
	keyWorkloads            = "workloads"
	keyTransactions         = "transactions"
	keyGlobal               = "global"
	keyDeletedAt            = "deleted_at"    // For deployment soft-delete
	keyTwinMetadata         = "twin_metadata" // For twin-level metadata
)

type MigrationStorage struct {
	unsafe BoltStorage
}

type BoltStorage struct {
	db     *bolt.DB
	unsafe bool
}

var _ provision.Storage = (*BoltStorage)(nil)

func New(path string) (*BoltStorage, error) {
	db, err := bolt.Open(path, 0644, bolt.DefaultOptions)
	if err != nil {
		return nil, err
	}

	return &BoltStorage{
		db, false,
	}, nil
}

func NewReadOnly(path string) (*BoltStorage, error) {
	db, err := bolt.Open(path, 0644, &bolt.Options{ReadOnly: true})
	if err != nil {
		return nil, err
	}

	return &BoltStorage{
		db, false,
	}, nil
}

func (b BoltStorage) Migration() MigrationStorage {
	b.unsafe = true
	return MigrationStorage{unsafe: b}
}

func (b *BoltStorage) Create(deployment gridtypes.Deployment) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		twin, err := tx.CreateBucketIfNotExists(b.u32(deployment.TwinID))
		if err != nil {
			return errors.Wrap(err, "failed to create twin")
		}

		// If twin was soft-deleted, revive it by clearing the deletion flag
		if b.isTwinBucketDeleted(twin) {
			if err := twin.DeleteBucket([]byte(keyTwinMetadata)); err != nil && !errors.Is(err, bolt.ErrBucketNotFound) {
				return errors.Wrap(err, "failed to revive twin")
			}
		}

		dl, err := twin.CreateBucket(b.u64(deployment.ContractID))
		if errors.Is(err, bolt.ErrBucketExists) {
			return provision.ErrDeploymentExists
		} else if err != nil {
			return errors.Wrap(err, "failed to create deployment")
		}

		if err := dl.Put([]byte(keyVersion), b.u32(deployment.Version)); err != nil {
			return err
		}
		if err := dl.Put([]byte(keyDescription), []byte(deployment.Description)); err != nil {
			return err
		}
		if err := dl.Put([]byte(keyMetadata), []byte(deployment.Metadata)); err != nil {
			return err
		}
		sig, err := json.Marshal(deployment.SignatureRequirement)
		if err != nil {
			return errors.Wrap(err, "failed to encode signature requirement")
		}
		if err := dl.Put([]byte(keySignatureRequirement), sig); err != nil {
			return err
		}

		for _, wl := range deployment.Workloads {
			if err := b.add(tx, deployment.TwinID, deployment.ContractID, wl); err != nil {
				return err
			}
		}
		return nil
	})
}

func (b *BoltStorage) Update(twin uint32, deployment uint64, field ...provision.Field) error {
	return b.db.Update(func(t *bolt.Tx) error {
		twin := t.Bucket(b.u32(twin))
		if twin == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
		}
		deployment := twin.Bucket(b.u64(deployment))
		if deployment == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
		}

		for _, field := range field {
			var key, value []byte
			switch f := field.(type) {
			case provision.VersionField:
				key = []byte(keyVersion)
				value = b.u32(f.Version)
			case provision.MetadataField:
				key = []byte(keyMetadata)
				value = []byte(f.Metadata)
			case provision.DescriptionField:
				key = []byte(keyDescription)
				value = []byte(f.Description)
			case provision.SignatureRequirementField:
				key = []byte(keySignatureRequirement)
				var err error
				value, err = json.Marshal(f.SignatureRequirement)
				if err != nil {
					return errors.Wrap(err, "failed to serialize signature requirements")
				}
			default:
				return fmt.Errorf("unknown field")
			}

			if err := deployment.Put(key, value); err != nil {
				return errors.Wrapf(err, "failed to update deployment")
			}
		}

		return nil
	})
}

// Migrate deployment creates an exact copy of dl in this storage.
// usually used to copy deployment from older storage
func (b *MigrationStorage) Migrate(dl gridtypes.Deployment) error {
	err := b.unsafe.Create(dl)
	if errors.Is(err, provision.ErrDeploymentExists) {
		log.Debug().Uint32("twin", dl.TwinID).Uint64("deployment", dl.ContractID).Msg("deployment already migrated")
		return nil
	} else if err != nil {
		return err
	}

	for _, wl := range dl.Workloads {
		if err := b.unsafe.Transaction(dl.TwinID, dl.ContractID, wl); err != nil {
			return err
		}
		if wl.Result.State == gridtypes.StateDeleted {
			if err := b.unsafe.Remove(dl.TwinID, dl.ContractID, wl.Name); err != nil {
				return err
			}
		}
	}

	return nil
}

func (b *BoltStorage) Delete(twin uint32, deployment uint64) error {
	return b.db.Update(func(t *bolt.Tx) error {
		twinBucket := t.Bucket(b.u32(twin))
		if twinBucket == nil {
			return nil
		}

		deploymentBucket := twinBucket.Bucket(b.u64(deployment))
		if deploymentBucket == nil {
			return nil
		}

		// Soft-delete: Set deleted_at timestamp instead of deleting bucket
		now := b.u64(uint64(gridtypes.Now()))
		if err := deploymentBucket.Put([]byte(keyDeletedAt), now); err != nil {
			return err
		}

		// Check if all deployments in twin are deleted → mark twin as deleted
		allDeleted := true
		cursor := twinBucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			if v != nil {
				// Not a bucket
				continue
			}

			if len(k) != 8 || string(k) == "global" {
				// Skip non-deployment buckets
				continue
			}

			dlBucket := twinBucket.Bucket(k)
			if dlBucket == nil {
				continue
			}

			// Check if this deployment is deleted
			deletedAt := dlBucket.Get([]byte(keyDeletedAt))
			if deletedAt == nil || b.l64(deletedAt) == 0 {
				// Found non-deleted deployment
				allDeleted = false
				break
			}
		}

		if allDeleted {
			// Mark twin as deleted
			twinMeta, err := twinBucket.CreateBucketIfNotExists([]byte(keyTwinMetadata))
			if err != nil {
				return errors.Wrap(err, "failed to create twin metadata bucket")
			}
			if err := twinMeta.Put([]byte(keyDeletedAt), now); err != nil {
				return err
			}
		}

		return nil
	})
}

func (b *BoltStorage) Get(twin uint32, deployment uint64, opts ...provision.QueryOpt) (dl gridtypes.Deployment, err error) {
	opts_ := &provision.QueryOpts{}
	for _, opt := range opts {
		opt(opts_)
	}

	dl.TwinID = twin
	dl.ContractID = deployment
	err = b.db.View(func(t *bolt.Tx) error {
		twin := t.Bucket(b.u32(twin))
		if twin == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
		}
		deployment := twin.Bucket(b.u64(deployment))
		if deployment == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
		}

		// Check if deployment is soft-deleted
		if !opts_.Deleted && b.isDeploymentBucketDeleted(deployment) {
			return errors.Wrap(provision.ErrDeploymentNotExists, "deployment is deleted")
		}

		if value := deployment.Get([]byte(keyVersion)); value != nil {
			dl.Version = b.l32(value)
		}
		if value := deployment.Get([]byte(keyDescription)); value != nil {
			dl.Description = string(value)
		}
		if value := deployment.Get([]byte(keyMetadata)); value != nil {
			dl.Metadata = string(value)
		}
		if value := deployment.Get([]byte(keySignatureRequirement)); value != nil {
			if err := json.Unmarshal(value, &dl.SignatureRequirement); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return dl, err
	}

	dl.Workloads, err = b.workloads(twin, deployment, opts_)
	return
}

func (b *BoltStorage) Error(twinID uint32, dl uint64, e error) error {
	current, err := b.Get(twinID, dl)
	if err != nil {
		return err
	}
	return b.db.Update(func(t *bolt.Tx) error {
		twin := t.Bucket(b.u32(twinID))
		if twin == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
		}
		deployment := twin.Bucket(b.u64(dl))
		if deployment == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
		}
		result := gridtypes.Result{
			Created: gridtypes.Now(),
			State:   gridtypes.StateError,
			Error:   e.Error(),
		}
		for _, wl := range current.Workloads {
			if err := b.transaction(t, twinID, dl, wl.WithResults(result)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (b *BoltStorage) add(tx *bolt.Tx, twinID uint32, dl uint64, workload gridtypes.Workload) error {
	global := gridtypes.IsSharable(workload.Type)
	twin := tx.Bucket(b.u32(twinID))
	if twin == nil {
		return errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
	}

	if global {
		shared, err := twin.CreateBucketIfNotExists([]byte(keyGlobal))
		if err != nil {
			return errors.Wrap(err, "failed to create twin global bucket")
		}

		if !b.unsafe {
			if value := shared.Get([]byte(workload.Name)); value != nil {
				return errors.Wrapf(
					provision.ErrDeploymentConflict, "global workload with the same name '%s' exists", workload.Name)
			}
		}

		if err := shared.Put([]byte(workload.Name), b.u64(dl)); err != nil {
			return err
		}
	}

	deployment := twin.Bucket(b.u64(dl))
	if deployment == nil {
		return errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
	}

	workloads, err := deployment.CreateBucketIfNotExists([]byte(keyWorkloads))
	if err != nil {
		return errors.Wrap(err, "failed to prepare workloads storage")
	}

	if value := workloads.Get([]byte(workload.Name)); value != nil {
		// Check if this is a soft-deleted workload
		if !isWorkloadDeleted(value) {
			// Active workload exists - cannot add duplicate
			return errors.Wrap(provision.ErrWorkloadExists, "workload with same name already exists in deployment")
		}
	}

	if err := workloads.Put([]byte(workload.Name), []byte(workload.Type.String())); err != nil {
		return err
	}

	return b.transaction(tx, twinID, dl,
		workload.WithResults(gridtypes.Result{
			Created: gridtypes.Now(),
			State:   gridtypes.StateInit,
		}),
	)
}

func (b *BoltStorage) Add(twin uint32, deployment uint64, workload gridtypes.Workload) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		return b.add(tx, twin, deployment, workload)
	})
}

func (b *BoltStorage) Remove(twin uint32, deployment uint64, name gridtypes.Name) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		twinBucket := tx.Bucket(b.u32(twin))
		if twinBucket == nil {
			return nil
		}

		deploymentBucket := twinBucket.Bucket(b.u64(deployment))
		if deploymentBucket == nil {
			return nil
		}

		workloads := deploymentBucket.Bucket([]byte(keyWorkloads))
		if workloads == nil {
			return nil
		}

		typ := workloads.Get([]byte(name))
		if typ == nil {
			return nil
		}

		// Clean up global bucket for sharable types
		if gridtypes.IsSharable(gridtypes.WorkloadType(typ)) {
			if shared := twinBucket.Bucket([]byte(keyGlobal)); shared != nil {
				if err := shared.Delete([]byte(name)); err != nil {
					return err
				}
			}
		}

		// Soft-delete: Mark workload as deleted with timestamp
		// Format: "type|deleted_at_unix_timestamp"
		deletedValue := fmt.Sprintf("%s|%d", typ, gridtypes.Now())
		return workloads.Put([]byte(name), []byte(deletedValue))
	})
}

func (b *BoltStorage) transaction(tx *bolt.Tx, twinID uint32, dl uint64, workload gridtypes.Workload) error {
	if err := workload.Result.Valid(); err != nil {
		return errors.Wrap(err, "failed to validate workload result")
	}

	data, err := json.Marshal(workload)
	if err != nil {
		return errors.Wrap(err, "failed to encode workload data")
	}

	twin := tx.Bucket(b.u32(twinID))
	if twin == nil {
		return errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
	}
	deployment := twin.Bucket(b.u64(dl))
	if deployment == nil {
		return errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
	}

	workloads := deployment.Bucket([]byte(keyWorkloads))
	if workloads == nil {
		return errors.Wrap(provision.ErrWorkloadNotExist, "deployment has no active workloads")
	}

	typRaw := workloads.Get([]byte(workload.Name))
	if typRaw == nil {
		return errors.Wrap(provision.ErrWorkloadNotExist, "workload does not exist")
	}

	if workload.Type != gridtypes.WorkloadType(typRaw) {
		return errors.Wrapf(ErrInvalidWorkloadType, "invalid workload type, expecting '%s'", string(typRaw))
	}

	logs, err := deployment.CreateBucketIfNotExists([]byte(keyTransactions))
	if err != nil {
		return errors.Wrap(err, "failed to prepare deployment transaction logs")
	}

	id, err := logs.NextSequence()
	if err != nil {
		return err
	}

	return logs.Put(b.u64(id), data)
}

func (b *BoltStorage) changes(tx *bolt.Tx, twinID uint32, dl uint64) ([]gridtypes.Workload, error) {
	twin := tx.Bucket(b.u32(twinID))
	if twin == nil {
		return nil, errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
	}
	deployment := twin.Bucket(b.u64(dl))
	if deployment == nil {
		return nil, errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
	}

	logs := deployment.Bucket([]byte(keyTransactions))
	if logs == nil {
		return nil, nil
	}
	var changes []gridtypes.Workload
	err := logs.ForEach(func(k, v []byte) error {
		if len(v) == 0 {
			return nil
		}

		var wl gridtypes.Workload
		if err := json.Unmarshal(v, &wl); err != nil {
			return errors.Wrap(err, "failed to load transaction log")
		}

		changes = append(changes, wl)
		return nil
	})

	return changes, err
}

func (b *BoltStorage) Transaction(twin uint32, deployment uint64, workload gridtypes.Workload) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		return b.transaction(tx, twin, deployment, workload)
	})
}

func (b *BoltStorage) Changes(twin uint32, deployment uint64) (changes []gridtypes.Workload, err error) {
	err = b.db.View(func(tx *bolt.Tx) error {
		changes, err = b.changes(tx, twin, deployment)
		return err
	})

	return
}

func (b *BoltStorage) workloads(twin uint32, deployment uint64, opts *provision.QueryOpts) ([]gridtypes.Workload, error) {
	names := make(map[gridtypes.Name]gridtypes.WorkloadType)
	workloads := make(map[gridtypes.Name]gridtypes.Workload)

	err := b.db.View(func(tx *bolt.Tx) error {
		twin := tx.Bucket(b.u32(twin))
		if twin == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
		}
		deployment := twin.Bucket(b.u64(deployment))
		if deployment == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
		}

		types := deployment.Bucket([]byte(keyWorkloads))
		if types == nil {
			// no active workloads
			return nil
		}

		err := types.ForEach(func(k, v []byte) error {
			typ, deleted := parseWorkloadType(v)
			if deleted && !opts.Deleted {
				return nil
			}

			names[gridtypes.Name(k)] = typ
			return nil
		})

		if err != nil {
			return err
		}

		if len(names) == 0 {
			return nil
		}

		logs := deployment.Bucket([]byte(keyTransactions))
		if logs == nil {
			// should we return an error instead?
			return nil
		}

		cursor := logs.Cursor()

		for k, v := cursor.Last(); k != nil; k, v = cursor.Prev() {
			var workload gridtypes.Workload
			if err := json.Unmarshal(v, &workload); err != nil {
				return errors.Wrap(err, "error while scanning transcation logs")
			}

			if _, ok := workloads[workload.Name]; ok {
				// already loaded and have last state
				continue
			}

			typ, ok := names[workload.Name]
			if !ok {
				// not an active workload
				continue
			}

			if workload.Type != typ {
				return fmt.Errorf("database inconsistency wrong workload type")
			}

			// otherwise we have a match.
			if workload.Result.State == gridtypes.StateUnChanged {
				continue
			}

			workloads[workload.Name] = workload
			if len(workloads) == len(names) {
				// we all latest states of active workloads
				break
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	if len(workloads) != len(names) {
		return nil, fmt.Errorf("inconsistency in deployment, missing workload transactions")
	}

	result := make([]gridtypes.Workload, 0, len(workloads))

	for _, wl := range workloads {
		result = append(result, wl)
	}

	return result, err
}

func (b *BoltStorage) Current(twin uint32, deployment uint64, name gridtypes.Name, opts ...provision.QueryOpt) (gridtypes.Workload, error) {
	opts_ := &provision.QueryOpts{}
	for _, opt := range opts {
		opt(opts_)
	}

	var workload gridtypes.Workload
	err := b.db.View(func(tx *bolt.Tx) error {
		twin := tx.Bucket(b.u32(twin))
		if twin == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
		}
		deployment := twin.Bucket(b.u64(deployment))
		if deployment == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
		}

		workloads := deployment.Bucket([]byte(keyWorkloads))
		if workloads == nil {
			return errors.Wrap(provision.ErrWorkloadNotExist, "deployment has no active workloads")
		}

		// this checks if this workload is an "active" workload.
		// if workload is not in this map, then workload might have been
		// deleted.
		typRaw := workloads.Get([]byte(name))
		if typRaw == nil {
			return errors.Wrap(provision.ErrWorkloadNotExist, "workload does not exist")
		}

		typ, deleted := parseWorkloadType(typRaw)
		if deleted && !opts_.Deleted {
			return errors.Wrap(provision.ErrWorkloadNotExist, "workload is deleted")
		}

		logs := deployment.Bucket([]byte(keyTransactions))
		if logs == nil {
			return errors.Wrap(ErrTransactionNotExist, "no transaction logs available")
		}

		cursor := logs.Cursor()

		found := false
		for k, v := cursor.Last(); k != nil; k, v = cursor.Prev() {
			if err := json.Unmarshal(v, &workload); err != nil {
				return errors.Wrap(err, "error while scanning transcation logs")
			}

			if workload.Name != name {
				continue
			}

			if workload.Type != typ {
				return fmt.Errorf("database inconsistency wrong workload type")
			}

			// otherwise we have a match.
			if workload.Result.State == gridtypes.StateUnChanged {
				continue
			}
			found = true
			break
		}

		if !found {
			return ErrTransactionNotExist
		}

		return nil
	})

	return workload, err
}

func (b *BoltStorage) Twins(opts ...provision.QueryOpt) ([]uint32, error) {
	opts_ := &provision.QueryOpts{}
	for _, opt := range opts {
		opt(opts_)
	}

	var twins []uint32
	err := b.db.View(func(t *bolt.Tx) error {
		cursor := t.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			if v != nil {
				// checking that it is a bucket
				continue
			}

			if len(k) != 4 {
				// sanity check it's a valid uint32
				continue
			}

			twinBucket := t.Bucket(k)
			if twinBucket == nil {
				continue
			}

			if !opts_.Deleted && b.isTwinBucketDeleted(twinBucket) {
				continue
			}

			twins = append(twins, b.l32(k))
		}

		return nil
	})

	return twins, err
}

func (b *BoltStorage) ByTwin(twin uint32, opts ...provision.QueryOpt) ([]uint64, error) {
	opts_ := &provision.QueryOpts{}
	for _, opt := range opts {
		opt(opts_)
	}

	var deployments []uint64
	err := b.db.View(func(t *bolt.Tx) error {
		bucket := t.Bucket(b.u32(twin))
		if bucket == nil {
			return nil
		}

		cursor := bucket.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			if v != nil {
				// checking that it is a bucket
				continue
			}

			if len(k) != 8 || string(k) == "global" || string(k) == keyTwinMetadata {
				// sanity check it's a valid deployment bucket
				continue
			}

			deploymentBucket := bucket.Bucket(k)
			if deploymentBucket == nil {
				continue
			}

			if !opts_.Deleted && b.isDeploymentBucketDeleted(deploymentBucket) {
				continue
			}

			deployments = append(deployments, b.l64(k))
		}

		return nil
	})

	return deployments, err
}

func (b *BoltStorage) Capacity(exclude ...provision.Exclude) (storageCap provision.StorageCapacity, err error) {
	twins, err := b.Twins()
	if err != nil {
		return provision.StorageCapacity{}, err
	}

	for _, twin := range twins {
		dls, err := b.ByTwin(twin)
		if err != nil {
			log.Error().Err(err).Uint32("twin", twin).Msg("failed to get twin deployments")
			continue
		}
		for _, dl := range dls {
			deployment, err := b.Get(twin, dl)
			if err != nil {
				log.Error().Err(err).Uint32("twin", twin).Uint64("deployment", dl).Msg("failed to get deployment")
				continue
			}

			isActive := false
		next:
			for _, wl := range deployment.Workloads {
				if !wl.Result.State.IsOkay() {
					continue
				}
				for _, exc := range exclude {
					if exc(&deployment, &wl) {
						continue next
					}
				}
				c, err := wl.Capacity()
				if err != nil {
					return provision.StorageCapacity{}, err
				}

				isActive = true
				storageCap.Workloads += 1
				storageCap.Cap.Add(&c)
				if wl.Result.Created > storageCap.LastDeploymentTimestamp {
					storageCap.LastDeploymentTimestamp = wl.Result.Created
				}
			}
			if isActive {
				storageCap.Deployments = append(storageCap.Deployments, deployment)
			}
		}
	}

	return storageCap, nil
}

// CleanDeleted hard-deletes items that were soft-deleted before the given timestamp.
// This purges old deleted items from the database to reclaim space.
func (b *BoltStorage) CleanDeleted(before gridtypes.Timestamp) error {
	beforeUnix := uint64(before)

	return b.db.Update(func(tx *bolt.Tx) error {
		cursor := tx.Cursor()

		// Iterate all twins
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			if v != nil {
				continue
			}
			if len(k) != 4 {
				continue
			}

			twinBucket := tx.Bucket(k)
			if twinBucket == nil {
				continue
			}

			// Iterate all deployments in this twin
			dlCursor := twinBucket.Cursor()
			for dlKey, dlVal := dlCursor.First(); dlKey != nil; dlKey, dlVal = dlCursor.Next() {
				if dlVal != nil {
					continue
				}
				if len(dlKey) != 8 || string(dlKey) == "global" || string(dlKey) == keyTwinMetadata {
					continue
				}

				deploymentBucket := twinBucket.Bucket(dlKey)
				if deploymentBucket == nil {
					continue
				}

				// Check if deployment is deleted and old enough
				if b.isDeploymentBucketDeleted(deploymentBucket) {
					deletedAt := deploymentBucket.Get([]byte(keyDeletedAt))
					if deletedAt != nil && b.l64(deletedAt) < beforeUnix {
						// Hard delete this deployment bucket
						deploymentID := b.l64(dlKey)
						twinID := b.l32(k)
						if err := twinBucket.DeleteBucket(dlKey); err != nil {
							log.Error().Err(err).Uint32("twin", twinID).Uint64("deployment", deploymentID).
								Msg("failed to hard delete deployment")
						}
						continue
					}
				}

				// Clean up deleted workloads within this deployment
				workloadsBucket := deploymentBucket.Bucket([]byte(keyWorkloads))
				b.cleanWorkloads(workloadsBucket, beforeUnix)
			}

			// Check if twin is deleted and old enough
			b.cleanTwin(tx, k, twinBucket, beforeUnix)
		}

		return nil
	})
}

func (b *BoltStorage) Close() error {
	return b.db.Close()
}
