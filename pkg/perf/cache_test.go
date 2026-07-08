package perf

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-co-op/gocron"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/threefoldtech/zos_base/pkg"
	"github.com/threefoldtech/zos_base/pkg/utils"
	"go.uber.org/mock/gomock"
)

func createTestTaskResult(name string, timestamp uint64) pkg.TaskResult {
	return pkg.TaskResult{
		Name:        name,
		Timestamp:   timestamp,
		Description: fmt.Sprintf("Test task %s", name),
		Result: map[string]interface{}{
			"test_data": fmt.Sprintf("result_for_%s", name),
			"value":     123,
		},
	}
}

func newTestPerformanceMonitorWithMockRedis(addr string) (*PerformanceMonitor, error) {
	scheduler := gocron.NewScheduler(time.UTC)

	redisURL := fmt.Sprintf("redis://%s", addr)
	redisPool, err := utils.NewRedisPool(redisURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating new redis pool")
	}

	return &PerformanceMonitor{
		scheduler:  scheduler,
		pool:       redisPool,
		zbusClient: nil, // Not needed for cache tests
		tasks:      []Task{},
	}, nil
}

func TestPerformanceMonitor_SetCache_Mock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	s := miniredis.RunT(t)

	pm, err := newTestPerformanceMonitorWithMockRedis(s.Addr())
	require.NoError(t, err)
	t.Run("successful setCache", func(t *testing.T) {
		ctx := context.Background()
		taskResult := createTestTaskResult("test-task", uint64(time.Now().Unix()))
		err := pm.setCache(ctx, taskResult)
		assert.NoError(t, err)
	})

	t.Run("setCache with Redis error", func(t *testing.T) {
		ctx := context.Background()
		taskResult := createTestTaskResult("test-task", uint64(time.Now().Unix()))
		s.Close()

		err := pm.setCache(ctx, taskResult)
		assert.Error(t, err)
	})
}

func TestPerformanceMonitor_Get_Mock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	s := miniredis.RunT(t)
	pm, err := newTestPerformanceMonitorWithMockRedis(s.Addr())
	require.NoError(t, err)

	t.Run("successful Get", func(t *testing.T) {
		taskResult := createTestTaskResult("test-task", uint64(time.Now().Unix()))

		ctx := context.Background()
		err := pm.setCache(ctx, taskResult)
		require.NoError(t, err)

		result, err := pm.Get("test-task")
		assert.NoError(t, err)
		assert.Equal(t, taskResult.Name, result.Name)
		assert.Equal(t, taskResult.Timestamp, result.Timestamp)
		assert.Equal(t, taskResult.Description, result.Description)
	})

	t.Run("Get non-existent key", func(t *testing.T) {
		result, err := pm.Get("non-existent")
		assert.Error(t, err)
		assert.Equal(t, ErrResultNotFound, err)
		assert.Empty(t, result.Name)
	})

	t.Run("Get with Redis error", func(t *testing.T) {
		s.Close()

		result, err := pm.Get("test-task")
		assert.Error(t, err)
		assert.Empty(t, result.Name)
	})

	t.Run("Get with invalid JSON", func(t *testing.T) {
		s2 := miniredis.RunT(t)
		pm2, err := newTestPerformanceMonitorWithMockRedis(s2.Addr())
		require.NoError(t, err)
		err = s2.Set(generateKey("test-task"), "{invalid json")
		require.NoError(t, err)
		result, err := pm2.Get("test-task")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal data from json")
		assert.Empty(t, result.Name)
	})
}

func TestPerformanceMonitor_Exists_Mock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	s := miniredis.RunT(t)
	pm, err := newTestPerformanceMonitorWithMockRedis(s.Addr())
	require.NoError(t, err)

	t.Run("exists returns true", func(t *testing.T) {
		ctx := context.Background()
		taskResult := createTestTaskResult("test-task", uint64(time.Now().Unix()))
		err := pm.setCache(ctx, taskResult)
		require.NoError(t, err)

		exists, err := pm.exists("test-task")
		assert.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("exists returns false", func(t *testing.T) {
		exists, err := pm.exists("non-existent")
		assert.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("exists with Redis error", func(t *testing.T) {
		s.Close()

		exists, err := pm.exists("test-task")
		assert.Error(t, err)
		assert.False(t, exists)
	})
}

func TestPerformanceMonitor_GetAll_Mock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	s := miniredis.RunT(t)
	pm, err := newTestPerformanceMonitorWithMockRedis(s.Addr())
	require.NoError(t, err)

	t.Run("successful GetAll with multiple results", func(t *testing.T) {
		ctx := context.Background()
		task1 := createTestTaskResult("task1", uint64(time.Now().Unix()))
		task2 := createTestTaskResult("task2", uint64(time.Now().Unix()-100))

		err := pm.setCache(ctx, task1)
		require.NoError(t, err)
		err = pm.setCache(ctx, task2)
		require.NoError(t, err)

		results, err := pm.GetAll()
		assert.NoError(t, err)
		assert.Len(t, results, 2)

		taskNames := []string{results[0].Name, results[1].Name}
		assert.Contains(t, taskNames, "task1")
		assert.Contains(t, taskNames, "task2")
	})

	t.Run("GetAll with no results", func(t *testing.T) {
		s2 := miniredis.RunT(t)
		pm2, err := newTestPerformanceMonitorWithMockRedis(s2.Addr())
		require.NoError(t, err)

		results, err := pm2.GetAll()
		assert.NoError(t, err)
		assert.Len(t, results, 0)
	})

	t.Run("GetAll with SCAN error", func(t *testing.T) {
		s.Close()

		results, err := pm.GetAll()
		assert.Error(t, err)
		assert.Nil(t, results)
	})

	t.Run("GetAll skips corrupted JSON", func(t *testing.T) {
		s3 := miniredis.RunT(t)
		pm3, err := newTestPerformanceMonitorWithMockRedis(s3.Addr())
		require.NoError(t, err)

		ctx := context.Background()
		validTask := createTestTaskResult("valid-task", uint64(time.Now().Unix()))
		err = pm3.setCache(ctx, validTask)
		require.NoError(t, err)

		err = s3.Set(generateKey("corrupted-task"), "{invalid json")
		require.NoError(t, err)

		results, err := pm3.GetAll()
		assert.NoError(t, err)
		assert.Len(t, results, 1) // Only valid task should be returned
		assert.Equal(t, "valid-task", results[0].Name)
	})
}

// Test generateKey helper function
func TestGenerateKey(t *testing.T) {
	testCases := []struct {
		taskName string
		expected string
	}{
		{"cpu-benchmark", "perf.cpu-benchmark"},
		{"public-ip-validation", "perf.public-ip-validation"},
		{"healthcheck", "perf.healthcheck"},
		{"iperf", "perf.iperf"},
		{"", "perf."},
	}

	for _, tc := range testCases {
		t.Run(tc.taskName, func(t *testing.T) {
			result := generateKey(tc.taskName)
			assert.Equal(t, tc.expected, result)
		})
	}
}
