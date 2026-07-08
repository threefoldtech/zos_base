package perf

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-co-op/gocron"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/threefoldtech/zos_base/pkg/mocks"
	"github.com/threefoldtech/zos_base/pkg/utils"
	"go.uber.org/mock/gomock"
)

// MockTask for testing
type MockTask struct {
	id          string
	cron        string
	description string
	jitter      uint32
	runFunc     func(context.Context) (interface{}, error)
}

func (m *MockTask) ID() string {
	return m.id
}

func (m *MockTask) Cron() string {
	return m.cron
}

func (m *MockTask) Description() string {
	return m.description
}

func (m *MockTask) Jitter() uint32 {
	return m.jitter
}

func (m *MockTask) Run(ctx context.Context) (interface{}, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx)
	}
	return "test-result", nil
}

func TestNewPerformanceMonitor(t *testing.T) {
	t.Run("create PerformanceMonitor successfully", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		server, err := miniredis.Run()
		require.NoError(t, err)
		defer server.Close()

		redisAddr := "tcp://" + server.Addr()
		pm, err := NewPerformanceMonitor(redisAddr)
		require.NoError(t, err)
		assert.NotNil(t, pm)
		assert.NotNil(t, pm.pool)
		assert.NotNil(t, pm.zbusClient)
		assert.NotNil(t, pm.scheduler)
		assert.Empty(t, pm.tasks)
	})

	t.Run("fail to create PerformanceMonitor with invalid redis address", func(t *testing.T) {
		_, err := NewPerformanceMonitor("invalid-redis-address")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed creating new redis pool")
	})
}

func TestPerformanceMonitor_AddTask(t *testing.T) {
	pm := &PerformanceMonitor{
		scheduler: gocron.NewScheduler(time.UTC),
		tasks:     []Task{},
	}

	task1 := &MockTask{id: "task1", cron: "* * * * * *"}
	task2 := &MockTask{id: "task2", cron: "0 * * * * *"}

	pm.AddTask(task1)
	assert.Len(t, pm.tasks, 1)
	assert.Equal(t, "task1", pm.tasks[0].ID())

	pm.AddTask(task2)
	assert.Len(t, pm.tasks, 2)
	assert.Equal(t, "task2", pm.tasks[1].ID())
}
func TestPerformanceMonitor_Run(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	redisAddr := "tcp://" + server.Addr()

	pool, err := utils.NewRedisPool(redisAddr)
	require.NoError(t, err)

	createMonitor := func(t *testing.T) (*PerformanceMonitor, *mocks.MockClient) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockZbus := mocks.NewMockClient(ctrl)

		return &PerformanceMonitor{
			scheduler:  gocron.NewScheduler(time.UTC),
			pool:       pool,
			zbusClient: mockZbus,
			tasks:      []Task{},
		}, mockZbus
	}

	t.Run("run with no tasks", func(t *testing.T) {
		pm, _ := createMonitor(t)
		ctx := context.Background()

		err := pm.Run(ctx)
		assert.NoError(t, err)

		assert.True(t, pm.scheduler.IsRunning())
		pm.scheduler.Stop()

		assert.False(t, pm.scheduler.IsRunning())
	})

	t.Run("run with invalid cron expression", func(t *testing.T) {
		pm, _ := createMonitor(t)

		task := &MockTask{
			id:          "invalid-task",
			cron:        "invalid-cron",
			description: "Invalid Task",
		}

		pm.AddTask(task)

		ctx := context.Background()

		err := pm.Run(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to schedule the task")
	})

	t.Run("run with task that doesn't exist in cache", func(t *testing.T) {
		pm, _ := createMonitor(t)

		taskExecuted := false
		task := &MockTask{
			id:          "new-task",
			cron:        "0 0 */6 * * *", // Every 6 hours (won't trigger during test)
			description: "New Task",
			jitter:      0,
			runFunc: func(ctx context.Context) (interface{}, error) {
				taskExecuted = true
				return "immediate-execution-result", nil
			},
		}

		pm.AddTask(task)

		ctx := context.Background()

		err = pm.Run(ctx)
		assert.NoError(t, err)

		jobs := pm.scheduler.Jobs()
		assert.Len(t, jobs, 1)

		time.Sleep(100 * time.Millisecond)

		assert.True(t, taskExecuted)

		assert.True(t, pm.scheduler.IsRunning())

		pm.scheduler.Stop()
	})

	t.Run("run with task that exists in cache", func(t *testing.T) {
		pm, _ := createMonitor(t)

		conn := pool.Get()
		_, err = conn.Do("SET", "perf.existing-task", "cached-result") // Use the correct key format with module prefix
		require.NoError(t, err)
		conn.Close()

		taskExecuted := false
		task := &MockTask{
			id:          "existing-task",
			cron:        "0 0 */6 * * *", // Every 6 hours (won't trigger during test)
			description: "Existing Task",
			jitter:      0,
			runFunc: func(ctx context.Context) (interface{}, error) {
				taskExecuted = true
				return "should-not-execute-immediately", nil
			},
		}

		pm.AddTask(task)

		ctx := context.Background()

		err = pm.Run(ctx)
		assert.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		assert.False(t, taskExecuted)

		assert.True(t, pm.scheduler.IsRunning())

		pm.scheduler.Stop()
	})

	t.Run("run with multiple tasks - some scheduled, some immediate", func(t *testing.T) {
		pm, _ := createMonitor(t)

		conn := pool.Get()
		_, err = conn.Do("SET", "perf.cached-task", "cached-result")
		require.NoError(t, err)
		conn.Close()

		task1Executed := false
		task2Executed := false

		// Task that exists in cache - should not execute immediately
		task1 := &MockTask{
			id:          "cached-task",
			cron:        "0 0 */12 * * *", // Every 12 hours
			description: "Cached Task",
			jitter:      0,
			runFunc: func(ctx context.Context) (interface{}, error) {
				task1Executed = true
				return "cached-task-result", nil
			},
		}

		// Task that doesn't exist in cache - should execute immediately
		task2 := &MockTask{
			id:          "new-task-1",
			cron:        "0 0 */8 * * *", // Every 8 hours
			description: "New Task 1",
			jitter:      0,
			runFunc: func(ctx context.Context) (interface{}, error) {
				task2Executed = true
				return "new-task-1-result", nil
			},
		}

		pm.AddTask(task1)
		pm.AddTask(task2)

		ctx := context.Background()

		err = pm.Run(ctx)
		assert.NoError(t, err)

		jobs := pm.scheduler.Jobs()
		assert.Len(t, jobs, 2)

		assert.True(t, pm.scheduler.IsRunning())

		time.Sleep(200 * time.Millisecond)

		assert.False(t, task1Executed, "cached task should not execute immediately")
		assert.True(t, task2Executed, "new task 2 should execute immediately")

		pm.scheduler.Stop()
	})
}
