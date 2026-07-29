package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/threefoldtech/zos_base/pkg/gridtypes"
	"github.com/threefoldtech/zos_base/pkg/provision"
	bolt "go.etcd.io/bbolt"
)

const (
	testType1         = gridtypes.WorkloadType("type1")
	testType2         = gridtypes.WorkloadType("type2")
	testSharableType1 = gridtypes.WorkloadType("sharable1")
)

type TestData struct{}

func (t TestData) Valid(getter gridtypes.WorkloadGetter) error {
	return nil
}

func (t TestData) Challenge(w io.Writer) error {
	return nil
}

func (t TestData) Capacity() (gridtypes.Capacity, error) {
	return gridtypes.Capacity{}, nil
}

func init() {
	gridtypes.RegisterType(testType1, TestData{})
	gridtypes.RegisterType(testType2, TestData{})
	gridtypes.RegisterSharableType(testSharableType1, TestData{})
}

func TestCreateDeployment(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
	}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Create(dl)
	require.ErrorIs(err, provision.ErrDeploymentExists)
}

func TestCreateDeploymentWithWorkloads(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
		Workloads: []gridtypes.Workload{
			{
				Type: testType1,
				Name: "vm1",
			},
			{
				Type: testType2,
				Name: "vm2",
			},
		},
	}

	err = db.Create(dl)
	require.NoError(err)

	err = db.Create(dl)
	require.ErrorIs(err, provision.ErrDeploymentExists)

	loaded, err := db.Get(1, 10)
	require.NoError(err)
	require.Len(loaded.Workloads, 2)
}

func TestCreateDeploymentWithSharableWorkloads(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
		Workloads: []gridtypes.Workload{
			{
				Type: testType1,
				Name: "vm1",
			},
			{
				Type: testSharableType1,
				Name: "network",
			},
		},
	}

	err = db.Create(dl)
	require.NoError(err)

	dl.ContractID = 11
	err = db.Create(dl)
	require.ErrorIs(err, provision.ErrDeploymentConflict)

	require.NoError(db.Remove(1, 10, "networkd"))
	err = db.Create(dl)
	require.ErrorIs(err, provision.ErrDeploymentConflict)

}

func TestAddWorkload(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.ErrorIs(err, provision.ErrDeploymentNotExists)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
	}

	err = db.Create(dl)
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.ErrorIs(err, provision.ErrWorkloadExists)
}

func TestRemoveWorkload(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
	}

	err = db.Create(dl)
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

	err = db.Remove(1, 10, "vm1")
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

}

func TestTransactions(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
	}

	err = db.Create(dl)
	require.NoError(err)

	_, err = db.Current(1, 10, "vm1")
	require.ErrorIs(err, provision.ErrWorkloadNotExist)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

	wl, err := db.Current(1, 10, "vm1")
	require.NoError(err)
	require.Equal(gridtypes.StateInit, wl.Result.State)

	err = db.Transaction(1, 10, gridtypes.Workload{
		Type: testType1,
		Name: gridtypes.Name("wrong"), // wrong name
		Result: gridtypes.Result{
			Created: gridtypes.Now(),
			State:   gridtypes.StateOk,
		},
	})

	require.ErrorIs(err, provision.ErrWorkloadNotExist)

	err = db.Transaction(1, 10, gridtypes.Workload{
		Type: testType2, // wrong type
		Name: gridtypes.Name("vm1"),
		Result: gridtypes.Result{
			Created: gridtypes.Now(),
			State:   gridtypes.StateOk,
		},
	})

	require.ErrorIs(err, ErrInvalidWorkloadType)

	err = db.Transaction(1, 10, gridtypes.Workload{
		Type: testType1,
		Name: gridtypes.Name("vm1"),
		Result: gridtypes.Result{
			Created: gridtypes.Now(),
			State:   gridtypes.StateOk,
		},
	})

	require.NoError(err)

	wl, err = db.Current(1, 10, "vm1")
	require.NoError(err)
	require.Equal(gridtypes.Name("vm1"), wl.Name)
	require.Equal(testType1, wl.Type)
	require.Equal(gridtypes.StateOk, wl.Result.State)
}

func TestTwins(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
	}

	err = db.Create(dl)
	require.NoError(err)

	dl.TwinID = 2

	err = db.Create(dl)
	require.NoError(err)

	twins, err := db.Twins()
	require.NoError(err)

	require.Len(twins, 2)
	require.EqualValues(1, twins[0])
	require.EqualValues(2, twins[1])
}

func TestGet(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
	}

	err = db.Create(dl)
	require.NoError(err)

	require.NoError(db.Add(dl.TwinID, dl.ContractID, gridtypes.Workload{Name: "vm1", Type: testType1}))
	require.NoError(db.Add(dl.TwinID, dl.ContractID, gridtypes.Workload{Name: "vm2", Type: testType2}))

	loaded, err := db.Get(1, 10)
	require.NoError(err)

	require.EqualValues(1, loaded.Version)
	require.EqualValues(1, loaded.TwinID)
	require.EqualValues(10, loaded.ContractID)
	require.EqualValues("description", loaded.Description)
	require.EqualValues("some metadata", loaded.Metadata)
	require.Len(loaded.Workloads, 2)
}

func TestError(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	someError := fmt.Errorf("something is wrong")
	err = db.Error(1, 10, someError)
	require.ErrorIs(err, provision.ErrDeploymentNotExists)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
		Workloads: []gridtypes.Workload{
			{Name: "vm1", Type: testType1},
		},
	}

	err = db.Create(dl)
	require.NoError(err)

	err = db.Error(1, 10, someError)
	require.NoError(err)

	loaded, err := db.Get(1, 10)
	require.NoError(err)
	require.Equal(gridtypes.StateError, loaded.Workloads[0].Result.State)
	require.Equal(someError.Error(), loaded.Workloads[0].Result.Error)
}

func TestMigrate(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
		Workloads: []gridtypes.Workload{
			{
				Name: "vm1",
				Type: testType1,
				Data: json.RawMessage("null"),
				Result: gridtypes.Result{
					Created: gridtypes.Now(),
					State:   gridtypes.StateOk,
					Data:    json.RawMessage("\"hello\""),
				},
			},
			{
				Name: "vm2",
				Type: testType2,
				Data: json.RawMessage("\"input\""),
				Result: gridtypes.Result{
					Created: gridtypes.Now(),
					State:   gridtypes.StateError,
					Data:    json.RawMessage("null"),
					Error:   "some error",
				},
			},
		},
	}

	migration := db.Migration()
	err = migration.Migrate(dl)
	require.NoError(err)

	loaded, err := db.Get(1, 10)
	sort.Slice(loaded.Workloads, func(i, j int) bool {
		return loaded.Workloads[i].Name < loaded.Workloads[j].Name
	})

	require.NoError(err)
	require.EqualValues(dl, loaded)
}

func TestMigrateUnsafe(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	migration := db.Migration()

	require.False(db.unsafe)
	require.True(migration.unsafe.unsafe)
}

func TestDeleteDeployment(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
		Workloads: []gridtypes.Workload{
			{
				Type: testType1,
				Name: "vm1",
			},
			{
				Type: testType2,
				Name: "vm2",
			},
		},
	}

	err = db.Create(dl)
	require.NoError(err)

	err = db.Delete(1, 10)
	require.NoError(err)

	// Soft-delete: Get() should return error
	_, err = db.Get(1, 10)
	require.ErrorIs(err, provision.ErrDeploymentNotExists)

	// Soft-delete: ByTwin() should return empty (filtered)
	deployments, err := db.ByTwin(1)
	require.NoError(err)
	require.Empty(deployments)

	// Soft-delete: Twin bucket should still exist but marked as deleted
	err = db.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(db.u32(1))
		if bucket == nil {
			return fmt.Errorf("twin bucket was deleted (should be soft-deleted)")
		}
		return nil
	})
	require.NoError(err)

	// Verify twin is marked as deleted
	isDeleted, _, err := db.IsTwinDeleted(1)
	require.NoError(err)
	require.True(isDeleted)
}

func TestDeleteDeploymentMultiple(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "description",
		Metadata:    "some metadata",
		Workloads: []gridtypes.Workload{
			{
				Type: testType1,
				Name: "vm1",
			},
			{
				Type: testType2,
				Name: "vm2",
			},
		},
	}

	err = db.Create(dl)
	require.NoError(err)

	dl.ContractID = 20
	err = db.Create(dl)
	require.NoError(err)

	err = db.Delete(1, 10)
	require.NoError(err)

	_, err = db.Get(1, 10)
	require.ErrorIs(err, provision.ErrDeploymentNotExists)
	deployments, err := db.ByTwin(1)
	require.NoError(err)
	require.Len(deployments, 1)

	_, err = db.Get(1, 20)
	require.NoError(err)
}

// Soft-Delete Tests

func TestSoftDeleteDeployment(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "test deployment",
		Workloads: []gridtypes.Workload{
			{Type: testType1, Name: "vm1"},
		},
	}

	err = db.Create(dl)
	require.NoError(err)

	// Delete deployment (soft-delete)
	err = db.Delete(1, 10)
	require.NoError(err)

	// Get() should fail on soft-deleted deployment
	_, err = db.Get(1, 10)
	require.ErrorIs(err, provision.ErrDeploymentNotExists)

	// Get() with WithDeleted() should work
	deleted, err := db.Get(1, 10, provision.WithDeleted())
	require.NoError(err)
	require.Equal(uint32(1), deleted.TwinID)
	require.Equal(uint64(10), deleted.ContractID)

	// Check deletion status
	isDeleted, deletedAt, err := db.IsDeploymentDeleted(1, 10)
	require.NoError(err)
	require.True(isDeleted)
	require.Greater(int64(deletedAt), int64(0))
}

func TestSoftDeleteTwin(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment
	dl := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
		Workloads:  []gridtypes.Workload{{Type: testType1, Name: "vm1"}},
	}
	err = db.Create(dl)
	require.NoError(err)

	// Delete deployment - twin should be marked as deleted
	err = db.Delete(1, 10)
	require.NoError(err)

	// Twin should not appear in Twins()
	twins, err := db.Twins()
	require.NoError(err)
	require.Empty(twins)

	// But should appear in Twins(WithDeleted())
	allTwins, err := db.Twins(provision.WithDeleted())
	require.NoError(err)
	require.Len(allTwins, 1)
	require.Equal(uint32(1), allTwins[0])

	// Check twin deletion status
	isDeleted, deletedAt, err := db.IsTwinDeleted(1)
	require.NoError(err)
	require.True(isDeleted)
	require.Greater(int64(deletedAt), int64(0))
}

func TestSoftDeleteWorkload(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
	}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

	// Remove workload (soft-delete)
	err = db.Remove(1, 10, "vm1")
	require.NoError(err)

	// Current() should fail on soft-deleted workload
	_, err = db.Current(1, 10, "vm1")
	require.ErrorIs(err, provision.ErrWorkloadNotExist)

	// Current() with WithDeleted() should work
	wl, err := db.Current(1, 10, "vm1", provision.WithDeleted())
	require.NoError(err)
	require.Equal(gridtypes.Name("vm1"), wl.Name)
	require.Equal(testType1, wl.Type)
}

func TestGetIgnoresDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
		Workloads:  []gridtypes.Workload{{Type: testType1, Name: "vm1"}},
	}
	err = db.Create(dl)
	require.NoError(err)

	// Verify we can get it before deletion
	_, err = db.Get(1, 10)
	require.NoError(err)

	// Delete it
	err = db.Delete(1, 10)
	require.NoError(err)

	// Get() should fail
	_, err = db.Get(1, 10)
	require.ErrorIs(err, provision.ErrDeploymentNotExists)
}

func TestTwinsIgnoresDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create two twins
	dl1 := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
	}
	dl2 := gridtypes.Deployment{
		Version:    1,
		TwinID:     2,
		ContractID: 20,
	}
	err = db.Create(dl1)
	require.NoError(err)
	err = db.Create(dl2)
	require.NoError(err)

	// Should see both twins
	twins, err := db.Twins()
	require.NoError(err)
	require.Len(twins, 2)

	// Delete twin 1
	err = db.Delete(1, 10)
	require.NoError(err)

	// Should only see twin 2
	twins, err = db.Twins()
	require.NoError(err)
	require.Len(twins, 1)
	require.Equal(uint32(2), twins[0])
}

func TestByTwinIgnoresDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create two deployments
	dl1 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	dl2 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 20}
	err = db.Create(dl1)
	require.NoError(err)
	err = db.Create(dl2)
	require.NoError(err)

	// Should see both
	deployments, err := db.ByTwin(1)
	require.NoError(err)
	require.Len(deployments, 2)

	// Delete one
	err = db.Delete(1, 10)
	require.NoError(err)

	// Should only see the other
	deployments, err = db.ByTwin(1)
	require.NoError(err)
	require.Len(deployments, 1)
	require.Equal(uint64(20), deployments[0])
}

func TestAllTwinsIncludesDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
	}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Delete(1, 10)
	require.NoError(err)

	// Twins() should be empty
	twins, err := db.Twins()
	require.NoError(err)
	require.Empty(twins)

	// Twins(WithDeleted()) should include deleted
	allTwins, err := db.Twins(provision.WithDeleted())
	require.NoError(err)
	require.Len(allTwins, 1)
	require.Equal(uint32(1), allTwins[0])
}

func TestAllDeploymentsIncludesDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl1 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	dl2 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 20}
	err = db.Create(dl1)
	require.NoError(err)
	err = db.Create(dl2)
	require.NoError(err)

	err = db.Delete(1, 10)
	require.NoError(err)

	// ByTwin() should only show active
	deployments, err := db.ByTwin(1)
	require.NoError(err)
	require.Len(deployments, 1)

	// ByTwin(WithDeleted()) should show all
	allDeployments, err := db.ByTwin(1, provision.WithDeleted())
	require.NoError(err)
	require.Len(allDeployments, 2)
}

func TestGetIncludingDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "test",
		Workloads:   []gridtypes.Workload{{Type: testType1, Name: "vm1"}},
	}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Delete(1, 10)
	require.NoError(err)

	// Get() fails
	_, err = db.Get(1, 10)
	require.Error(err)

	// Get(WithDeleted()) works
	deleted, err := db.Get(1, 10, provision.WithDeleted())
	require.NoError(err)
	require.Equal("test", deleted.Description)
	require.Len(deleted.Workloads, 1)
}

func TestAllWorkloadsIncludesDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)
	err = db.Add(1, 10, gridtypes.Workload{Name: "vm2", Type: testType2})
	require.NoError(err)

	// Remove one workload
	err = db.Remove(1, 10, "vm1")
	require.NoError(err)

	// Get() should only show active workload
	deployment, err := db.Get(1, 10)
	require.NoError(err)
	require.Len(deployment.Workloads, 1)
	require.Equal(gridtypes.Name("vm2"), deployment.Workloads[0].Name)

	// Get(WithDeleted()) should show both
	allWorkloads, err := db.Get(1, 10, provision.WithDeleted())
	require.NoError(err)
	require.Len(allWorkloads.Workloads, 2)
}

func TestCurrentIncludingDeletedFindsDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

	// Update to OK state
	err = db.Transaction(1, 10, gridtypes.Workload{
		Name: "vm1",
		Type: testType1,
		Result: gridtypes.Result{
			Created: gridtypes.Now(),
			State:   gridtypes.StateOk,
		},
	})
	require.NoError(err)

	// Remove workload
	err = db.Remove(1, 10, "vm1")
	require.NoError(err)

	// Current() should fail
	_, err = db.Current(1, 10, "vm1")
	require.Error(err)

	// Current(WithDeleted()) should work
	wl, err := db.Current(1, 10, "vm1", provision.WithDeleted())
	require.NoError(err)
	require.Equal(gridtypes.Name("vm1"), wl.Name)
	require.Equal(gridtypes.StateOk, wl.Result.State)
}

func TestIsDeploymentDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Not deleted initially
	isDeleted, _, err := db.IsDeploymentDeleted(1, 10)
	require.NoError(err)
	require.False(isDeleted)

	// Delete it
	err = db.Delete(1, 10)
	require.NoError(err)

	// Should be deleted now
	isDeleted, deletedAt, err := db.IsDeploymentDeleted(1, 10)
	require.NoError(err)
	require.True(isDeleted)
	require.Greater(int64(deletedAt), int64(0))
}

func TestIsTwinDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Not deleted initially
	isDeleted, _, err := db.IsTwinDeleted(1)
	require.NoError(err)
	require.False(isDeleted)

	// Delete deployment - twin should be marked deleted
	err = db.Delete(1, 10)
	require.NoError(err)

	// Should be deleted now
	isDeleted, deletedAt, err := db.IsTwinDeleted(1)
	require.NoError(err)
	require.True(isDeleted)
	require.Greater(int64(deletedAt), int64(0))
}

func TestCleanDeletedRemovesOld(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create and delete a deployment
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Delete(1, 10)
	require.NoError(err)

	// Clean up old deleted items (far future timestamp)
	future := gridtypes.Now() + 1000000
	err = db.CleanDeleted(future)
	require.NoError(err)

	// Even Get(WithDeleted()) should fail now
	_, err = db.Get(1, 10, provision.WithDeleted())
	require.Error(err)

	// Twins(WithDeleted()) should be empty
	allTwins, err := db.Twins(provision.WithDeleted())
	require.NoError(err)
	require.Empty(allTwins)
}

func TestCleanDeletedPreservesRecent(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create and delete a deployment
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Delete(1, 10)
	require.NoError(err)

	// Clean up items deleted before now (should preserve recent deletion)
	before := gridtypes.Now() - 1000
	err = db.CleanDeleted(before)
	require.NoError(err)

	// Get(WithDeleted()) should still work
	_, err = db.Get(1, 10, provision.WithDeleted())
	require.NoError(err)

	// Twins(WithDeleted()) should still show the twin
	allTwins, err := db.Twins(provision.WithDeleted())
	require.NoError(err)
	require.Len(allTwins, 1)
}

func TestCleanDeletedWorkloads(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

	// Remove workload
	err = db.Remove(1, 10, "vm1")
	require.NoError(err)

	// Verify it's soft-deleted
	deployment, err := db.Get(1, 10, provision.WithDeleted())
	require.NoError(err)
	require.Len(deployment.Workloads, 1)

	// Clean up old deletions
	future := gridtypes.Now() + 1000000
	err = db.CleanDeleted(future)
	require.NoError(err)

	// Get(WithDeleted()) should be empty now for workloads
	deployment, err = db.Get(1, 10, provision.WithDeleted())
	require.NoError(err)
	require.Empty(deployment.Workloads)
}

func TestCreateDeploymentUnderDeletedTwinRevivesTwin(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create first deployment for twin 1
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Delete the deployment - twin should be marked as deleted
	err = db.Delete(1, 10)
	require.NoError(err)

	// Verify twin is deleted
	isDeleted, deletedAt1, err := db.IsTwinDeleted(1)
	require.NoError(err)
	require.True(isDeleted)
	require.Greater(int64(deletedAt1), int64(0))

	// Create a new deployment under the same twin - should revive the twin
	dl2 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 20}
	err = db.Create(dl2)
	require.NoError(err)

	// Verify twin is no longer deleted
	isDeleted, _, err = db.IsTwinDeleted(1)
	require.NoError(err)
	require.False(isDeleted)

	// Verify the new deployment exists
	loaded, err := db.Get(1, 20)
	require.NoError(err)
	require.Equal(uint32(1), loaded.TwinID)
	require.Equal(uint64(20), loaded.ContractID)

	// Verify the twin appears in Twins() now
	twins, err := db.Twins()
	require.NoError(err)
	require.Len(twins, 1)
	require.Equal(uint32(1), twins[0])
}

// Group 1: New Changes (Twin Revival & Soft-delete)

func TestCreateAfterCleanDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create twin=1, contract=10
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Delete it
	err = db.Delete(1, 10)
	require.NoError(err)

	// CleanDeleted with future timestamp (purges the bucket entirely)
	future := gridtypes.Now() + 1000000
	err = db.CleanDeleted(future)
	require.NoError(err)

	// Create again for same twin with new contract=20
	dl2 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 20}
	err = db.Create(dl2)
	require.NoError(err)

	// Verify no error and IsTwinDeleted returns false
	isDeleted, _, err := db.IsTwinDeleted(1)
	require.NoError(err)
	require.False(isDeleted)

	// Verify we can Get the new deployment
	loaded, err := db.Get(1, 20)
	require.NoError(err)
	require.Equal(uint64(20), loaded.ContractID)
}

func TestDeleteNoOpOnMissingTwin(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Delete non-existent twin 999
	err = db.Delete(999, 10)
	require.NoError(err) // Should be no-op, no error
}

func TestDeleteNoOpOnMissingDeployment(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create twin 1
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Delete non-existent deployment 999 under twin 1
	err = db.Delete(1, 999)
	require.NoError(err) // Should be no-op, no error
}

func TestDeleteWithSharableWorkloadMarksAllDeleted(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment with sharable workload
	dl := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
		Workloads: []gridtypes.Workload{
			{Type: testSharableType1, Name: "net"},
		},
	}
	err = db.Create(dl)
	require.NoError(err)

	// Delete the deployment
	err = db.Delete(1, 10)
	require.NoError(err)

	// Twin should be marked as deleted (cursor correctly skipped "global" sub-bucket)
	isDeleted, _, err := db.IsTwinDeleted(1)
	require.NoError(err)
	require.True(isDeleted)
}

func TestResurrectSharableWorkload(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment with sharable workload
	dl := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
		Workloads: []gridtypes.Workload{
			{Type: testSharableType1, Name: "net"},
		},
	}
	err = db.Create(dl)
	require.NoError(err)

	// Remove the workload (soft-delete, clears global bucket)
	err = db.Remove(1, 10, "net")
	require.NoError(err)

	// Add it back (re-registers in global bucket)
	err = db.Add(1, 10, gridtypes.Workload{Type: testSharableType1, Name: "net"})
	require.NoError(err)

	// Verify workload is active
	wl, err := db.Current(1, 10, "net")
	require.NoError(err)
	require.Equal(gridtypes.StateInit, wl.Result.State)

	// Verify global bucket has the entry: trying to add to a second deployment
	// should trigger ErrDeploymentConflict
	dl2 := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 20,
		Workloads: []gridtypes.Workload{
			{Type: testSharableType1, Name: "net"},
		},
	}
	err = db.Create(dl2)
	require.ErrorIs(err, provision.ErrDeploymentConflict)
}

func TestCleanDeletedPreservesTwinWithRemainingDeployments(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create two deployments for same twin
	dl1 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	dl2 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 20}
	err = db.Create(dl1)
	require.NoError(err)
	err = db.Create(dl2)
	require.NoError(err)

	// Delete first deployment
	err = db.Delete(1, 10)
	require.NoError(err)

	// CleanDeleted with future timestamp
	future := gridtypes.Now() + 1000000
	err = db.CleanDeleted(future)
	require.NoError(err)

	// Deleted deployment should be purged
	_, err = db.Get(1, 10, provision.WithDeleted())
	require.Error(err)

	// Active deployment should still be reachable
	loaded, err := db.Get(1, 20)
	require.NoError(err)
	require.Equal(uint64(20), loaded.ContractID)

	// Twin should still exist and not be deleted (has remaining deployment)
	isDeleted, _, err := db.IsTwinDeleted(1)
	require.NoError(err)
	require.False(isDeleted)
}

func TestCleanDeletedLeavesActiveDeploymentBucketIntact(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment with active workload
	dl := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
		Workloads: []gridtypes.Workload{
			{Type: testType1, Name: "vm1"},
		},
	}
	err = db.Create(dl)
	require.NoError(err)

	// Remove workload (soft-delete)
	err = db.Remove(1, 10, "vm1")
	require.NoError(err)

	// CleanDeleted with future timestamp (purges deleted workload but not deployment)
	future := gridtypes.Now() + 1000000
	err = db.CleanDeleted(future)
	require.NoError(err)

	// Deployment should still be reachable and intact
	loaded, err := db.Get(1, 10)
	require.NoError(err)
	require.Equal(uint32(1), loaded.TwinID)
	require.Equal(uint64(10), loaded.ContractID)
}

// Group 2: Error & No-op Paths

func TestRemoveNoOpOnMissingWorkload(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Remove non-existent workload
	err = db.Remove(1, 10, "nonexistent")
	require.NoError(err) // No-op, should return nil
}

func TestRemoveNoOpOnMissingTwin(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Remove from non-existent twin
	err = db.Remove(999, 10, "vm1")
	require.NoError(err) // No-op, should return nil
}

func TestRemoveSharableWorkloadCleansGlobalBucket(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment with sharable workload
	dl1 := gridtypes.Deployment{
		Version:    1,
		TwinID:     1,
		ContractID: 10,
		Workloads: []gridtypes.Workload{
			{Type: testSharableType1, Name: "net"},
		},
	}
	err = db.Create(dl1)
	require.NoError(err)

	// Create second empty deployment
	dl2 := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 20}
	err = db.Create(dl2)
	require.NoError(err)

	// Remove sharable workload from first deployment
	err = db.Remove(1, 10, "net")
	require.NoError(err)

	// Add same sharable name to second deployment - should succeed (global entry was cleared)
	err = db.Add(1, 20, gridtypes.Workload{Type: testSharableType1, Name: "net"})
	require.NoError(err)
}

func TestIsTwinDeletedNotFound(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// IsTwinDeleted on non-existent twin
	_, _, err = db.IsTwinDeleted(999)
	require.Error(err)
}

func TestIsDeploymentDeletedNotFound(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create twin 1
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// IsDeploymentDeleted on non-existent twin
	_, _, err = db.IsDeploymentDeleted(999, 10)
	require.ErrorIs(err, provision.ErrDeploymentNotExists)

	// IsDeploymentDeleted on non-existent deployment
	_, _, err = db.IsDeploymentDeleted(1, 999)
	require.ErrorIs(err, provision.ErrDeploymentNotExists)
}

func TestTransactionOnSoftDeletedWorkloadFails(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment with workload
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

	// Remove workload (soft-delete)
	err = db.Remove(1, 10, "vm1")
	require.NoError(err)

	// Transaction on soft-deleted workload should fail
	err = db.Transaction(1, 10, gridtypes.Workload{
		Type: testType1,
		Name: "vm1",
		Result: gridtypes.Result{
			Created: gridtypes.Now(),
			State:   gridtypes.StateOk,
		},
	})
	require.ErrorIs(err, ErrInvalidWorkloadType)
}

// Group 3: Completely Uncovered Public Methods

func TestUpdate(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment
	dl := gridtypes.Deployment{
		Version:     1,
		TwinID:      1,
		ContractID:  10,
		Description: "original",
		Metadata:    "meta",
	}
	err = db.Create(dl)
	require.NoError(err)

	// Update version and description
	err = db.Update(1, 10,
		provision.VersionField{Version: 2},
		provision.DescriptionField{Description: "updated"},
	)
	require.NoError(err)

	// Verify changes
	loaded, err := db.Get(1, 10)
	require.NoError(err)
	require.Equal(uint32(2), loaded.Version)
	require.Equal("updated", loaded.Description)
	require.Equal("meta", loaded.Metadata)

	// Update metadata
	err = db.Update(1, 10,
		provision.MetadataField{Metadata: "new_meta"},
	)
	require.NoError(err)

	loaded, err = db.Get(1, 10)
	require.NoError(err)
	require.Equal("new_meta", loaded.Metadata)
}

func TestUpdateTwinNotFound(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Update non-existent twin
	err = db.Update(999, 10, provision.VersionField{Version: 2})
	require.ErrorIs(err, provision.ErrDeploymentNotExists)
}

func TestUpdateDeploymentNotFound(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create twin 1
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Update non-existent deployment
	err = db.Update(1, 999, provision.VersionField{Version: 2})
	require.ErrorIs(err, provision.ErrDeploymentNotExists)
}

func TestChanges(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Add workload
	err = db.Add(1, 10, gridtypes.Workload{Name: "vm1", Type: testType1})
	require.NoError(err)

	// Get changes - should have one entry (StateInit from Add)
	changes, err := db.Changes(1, 10)
	require.NoError(err)
	require.Len(changes, 1)
	require.Equal(gridtypes.Name("vm1"), changes[0].Name)
	require.Equal(gridtypes.StateInit, changes[0].Result.State)

	// Add a transaction
	err = db.Transaction(1, 10, gridtypes.Workload{
		Type: testType1,
		Name: "vm1",
		Result: gridtypes.Result{
			Created: gridtypes.Now(),
			State:   gridtypes.StateOk,
		},
	})
	require.NoError(err)

	// Get changes again - should have two entries
	changes, err = db.Changes(1, 10)
	require.NoError(err)
	require.Len(changes, 2)
	require.Equal(gridtypes.StateInit, changes[0].Result.State)
	require.Equal(gridtypes.StateOk, changes[1].Result.State)
}

func TestChangesNoWorkloads(t *testing.T) {
	require := require.New(t)
	path := filepath.Join(os.TempDir(), fmt.Sprint(rand.Int63()))
	defer os.RemoveAll(path)

	db, err := New(path)
	require.NoError(err)

	// Create deployment without workloads
	dl := gridtypes.Deployment{Version: 1, TwinID: 1, ContractID: 10}
	err = db.Create(dl)
	require.NoError(err)

	// Get changes - should be empty
	changes, err := db.Changes(1, 10)
	require.NoError(err)
	require.Empty(changes)
}
