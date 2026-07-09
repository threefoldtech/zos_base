package cpubench

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/threefoldtech/zbus"
	"github.com/threefoldtech/zos_base/pkg/mocks"
	"github.com/threefoldtech/zos_base/pkg/perf"
	execwrapper "github.com/threefoldtech/zos_base/pkg/perf/exec_wrapper"
	"go.uber.org/mock/gomock"
)

func TestCPUBenchmarkTask(t *testing.T) {
	t.Run("create new CPU benchmark task", func(t *testing.T) {
		task := NewTask()

		assert.NotNil(t, task)
		assert.Equal(t, "cpu-benchmark", task.ID())
		assert.Equal(t, "0 0 */6 * * *", task.Cron()) // Every 6 hours
		assert.Contains(t, task.Description(), "CPU")
		assert.Equal(t, uint32(0), task.Jitter())
	})

	t.Run("task implements perf.Task interface", func(t *testing.T) {
		var _ = NewTask()
	})
}

func TestCPUBenchmarkTask_Run(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockExec := execwrapper.NewMockExecWrapper(ctrl)
	mockCmd := execwrapper.NewMockExecCmd(ctrl)
	task := NewTaskWithExecWrapper(mockExec).(*CPUBenchmarkTask)
	ctx := context.Background()
	mockZbus := mocks.NewMockClient(ctrl)
	ctx = perf.WithZbusClient(ctx, mockZbus)

	mockExec.EXPECT().CommandContext(ctx, "cpubench", "-j").Return(mockCmd).Times(4)
	t.Run("successful benchmark execution", func(t *testing.T) {

		// Mock successful cpubench execution
		expectedOutput := `{
			"single": 1542.67,
			"multi": 6170.89,
			"threads": 4
		}`

		data := make([]byte, 8)
		binary.LittleEndian.PutUint64(data, 5)
		response := &zbus.Response{
			ID: "test-id",
			Output: zbus.Output{
				Data:  data,
				Error: nil,
			},
		}

		mockZbus.EXPECT().
			RequestContext(gomock.Any(), "provision", zbus.ObjectID{Name: "statistics", Version: "0.0.1"}, "Workloads").
			Return(response, nil)

		mockCmd.EXPECT().CombinedOutput().Return([]byte(expectedOutput), nil)

		result, err := task.Run(ctx)
		require.NoError(t, err)
		assert.NotNil(t, result)

		// Verify result structure
		cpuResult, ok := result.(CPUBenchmarkResult)
		require.True(t, ok, "Result should be of type CPUBenchmarkResult")

		assert.Equal(t, 1542.67, cpuResult.SingleThreaded)
		assert.Equal(t, 6170.89, cpuResult.MultiThreaded)
		assert.Equal(t, 4, cpuResult.Threads)
		assert.Equal(t, 5, cpuResult.Workloads)

	})

	t.Run("cpubench command fails", func(t *testing.T) {
		// Mock command failure
		mockCmd.EXPECT().CombinedOutput().Return([]byte{}, errors.New("command not found"))

		result, err := task.Run(ctx)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to execute cpubench command")
	})

	t.Run("invalid JSON output", func(t *testing.T) {

		// Mock command with invalid JSON output
		invalidJSON := `{invalid json output}`

		mockCmd.EXPECT().CombinedOutput().Return([]byte(invalidJSON), nil)

		result, err := task.Run(ctx)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to parse cpubench output")
	})

	t.Run("failed to get workloads number", func(t *testing.T) {

		mockCmd.EXPECT().CombinedOutput().Return([]byte(`{"single": 1000, "multi": 2000, "threads": 4}`), nil)
		// Mock failure in getting workloads number
		mockZbus.EXPECT().
			RequestContext(gomock.Any(), "provision", zbus.ObjectID{Name: "statistics", Version: "0.0.1"}, "Workloads").
			Return(nil, errors.New("failed to get workloads"))

		assert.Panics(t, func() {
			_, _ = task.Run(ctx)
		})
	})
}
