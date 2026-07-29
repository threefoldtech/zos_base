package storage

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zosbase/pkg/gridtypes"
	"github.com/threefoldtech/zosbase/pkg/provision"
	bolt "go.etcd.io/bbolt"
)

// isWorkloadDeleted checks if a workload value indicates soft-deletion.
// Format: "type" (active) or "type|timestamp" (deleted)
func isWorkloadDeleted(value []byte) bool {
	valueStr := string(value)
	parts := strings.Split(valueStr, "|")
	return len(parts) > 1 && parts[1] != ""
}

// parseWorkloadType extracts the workload type from a value that may be soft-deleted.
// Returns the type and whether the workload is deleted.
func parseWorkloadType(value []byte) (gridtypes.WorkloadType, bool) {
	valueStr := string(value)
	parts := strings.Split(valueStr, "|")
	deleted := len(parts) > 1 && parts[1] != ""
	return gridtypes.WorkloadType(parts[0]), deleted
}

// isDeploymentBucketDeleted checks if a deployment bucket has the deleted_at flag set.
func (b *BoltStorage) isDeploymentBucketDeleted(deployment *bolt.Bucket) bool {
	if deployment == nil {
		return false
	}
	deletedAt := deployment.Get([]byte(keyDeletedAt))
	return deletedAt != nil && b.l64(deletedAt) > 0
}

// isTwinBucketDeleted checks if a twin bucket has the deleted_at flag set.
func (b *BoltStorage) isTwinBucketDeleted(twin *bolt.Bucket) bool {
	if twin == nil {
		return false
	}
	meta := twin.Bucket([]byte(keyTwinMetadata))
	if meta == nil {
		return false
	}
	deletedAt := meta.Get([]byte(keyDeletedAt))
	return deletedAt != nil && b.l64(deletedAt) > 0
}

// deletedTimestamp returns the soft-delete timestamp encoded in a workload value,
// and whether the workload is deleted at all.
func deletedTimestamp(value []byte) (uint64, bool) {
	valueStr := string(value)
	parts := strings.Split(valueStr, "|")
	if len(parts) <= 1 || parts[1] == "" {
		return 0, false
	}
	var ts uint64
	if _, err := fmt.Sscanf(parts[1], "%d", &ts); err != nil {
		return 0, false
	}
	return ts, true
}

// Binary encoding/decoding helpers

func (b *BoltStorage) u32(u uint32) []byte {
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], u)
	return v[:]
}

func (b *BoltStorage) l32(v []byte) uint32 {
	return binary.BigEndian.Uint32(v)
}

func (b *BoltStorage) u64(u uint64) []byte {
	var v [8]byte
	binary.BigEndian.PutUint64(v[:], u)
	return v[:]
}

func (b *BoltStorage) l64(v []byte) uint64 {
	return binary.BigEndian.Uint64(v)
}

// cleanWorkloads removes per-workload soft-delete entries older than beforeUnix
// from the workloads sub-bucket of a deployment.
func (b *BoltStorage) cleanWorkloads(workloadsBucket *bolt.Bucket, beforeUnix uint64) {
	if workloadsBucket == nil {
		return
	}

	wlCursor := workloadsBucket.Cursor()
	for wlKey, wlVal := wlCursor.First(); wlKey != nil; wlKey, wlVal = wlCursor.Next() {
		if ts, deleted := deletedTimestamp(wlVal); deleted {
			if ts < beforeUnix {
				if err := workloadsBucket.Delete(wlKey); err != nil {
					log.Error().Err(err).Str("workload", string(wlKey)).
						Msg("failed to hard delete workload")
				}
			}
		}
	}
}

// cleanTwin hard-deletes the twin bucket if deleted, old enough, and has no remaining deployments.
func (b *BoltStorage) cleanTwin(tx *bolt.Tx, k []byte, twinBucket *bolt.Bucket, beforeUnix uint64) {
	if !b.isTwinBucketDeleted(twinBucket) {
		return
	}

	twinMeta := twinBucket.Bucket([]byte(keyTwinMetadata))
	if twinMeta == nil {
		return
	}

	deletedAt := twinMeta.Get([]byte(keyDeletedAt))
	if deletedAt == nil || b.l64(deletedAt) >= beforeUnix {
		return
	}

	// Check if twin has any deployments left
	hasDeployments := false
	checkCursor := twinBucket.Cursor()
	for chkKey, chkVal := checkCursor.First(); chkKey != nil; chkKey, chkVal = checkCursor.Next() {
		if chkVal != nil {
			continue
		}
		if len(chkKey) == 8 && string(chkKey) != "global" && string(chkKey) != keyTwinMetadata {
			hasDeployments = true
			break
		}
	}

	if !hasDeployments {
		twinID := b.l32(k)
		if err := tx.DeleteBucket(k); err != nil {
			log.Error().Err(err).Uint32("twin", twinID).Msg("failed to hard delete twin")
		}
	}
}

// IsDeploymentDeleted checks if a deployment is soft-deleted.
func (b *BoltStorage) IsDeploymentDeleted(twin uint32, deployment uint64) (deleted bool, deletedAt gridtypes.Timestamp, err error) {
	err = b.db.View(func(t *bolt.Tx) error {
		twinBucket := t.Bucket(b.u32(twin))
		if twinBucket == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "twin not found")
		}
		deploymentBucket := twinBucket.Bucket(b.u64(deployment))
		if deploymentBucket == nil {
			return errors.Wrap(provision.ErrDeploymentNotExists, "deployment not found")
		}

		if b.isDeploymentBucketDeleted(deploymentBucket) {
			if value := deploymentBucket.Get([]byte(keyDeletedAt)); value != nil {
				deleted = true
				deletedAt = gridtypes.Timestamp(b.l64(value))
			}
		}

		return nil
	})

	return
}

// IsTwinDeleted checks if a twin is soft-deleted.
func (b *BoltStorage) IsTwinDeleted(twin uint32) (deleted bool, deletedAt gridtypes.Timestamp, err error) {
	err = b.db.View(func(t *bolt.Tx) error {
		twinBucket := t.Bucket(b.u32(twin))
		if twinBucket == nil {
			return fmt.Errorf("twin not found")
		}

		if b.isTwinBucketDeleted(twinBucket) {
			meta := twinBucket.Bucket([]byte(keyTwinMetadata))
			if meta != nil {
				if value := meta.Get([]byte(keyDeletedAt)); value != nil {
					deleted = true
					deletedAt = gridtypes.Timestamp(b.l64(value))
				}
			}
		}

		return nil
	})

	return
}
