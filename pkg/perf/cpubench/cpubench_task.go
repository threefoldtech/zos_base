package cpubench

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/threefoldtech/zos_base/pkg/perf"
	execwrapper "github.com/threefoldtech/zos_base/pkg/perf/exec_wrapper"
	"github.com/threefoldtech/zos_base/pkg/stubs"
)

// CPUBenchmarkTask defines CPU benchmark task.
type CPUBenchmarkTask struct {
	execWrapper execwrapper.ExecWrapper
}

// CPUBenchmarkResult holds CPU benchmark results with the workloads number during the benchmark.
type CPUBenchmarkResult struct {
	SingleThreaded float64 `json:"single"`
	MultiThreaded  float64 `json:"multi"`
	Threads        int     `json:"threads"`
	Workloads      int     `json:"workloads"`
}

var _ perf.Task = (*CPUBenchmarkTask)(nil)

// NewTask returns a new CPU benchmark task.
func NewTask() perf.Task {
	return &CPUBenchmarkTask{
		execWrapper: &execwrapper.RealExecWrapper{},
	}
}

func NewTaskWithExecWrapper(execWrapper execwrapper.ExecWrapper) perf.Task {
	return &CPUBenchmarkTask{
		execWrapper: execWrapper,
	}
}

// ID returns task ID.
func (c *CPUBenchmarkTask) ID() string {
	return "cpu-benchmark"
}

// Cron returns task cron schedule.
func (c *CPUBenchmarkTask) Cron() string {
	return "0 0 */6 * * *"
}

// Description returns task description.
func (c *CPUBenchmarkTask) Description() string {
	return "Measures the performance of the node CPU by reporting the timespent of computing a task in seconds."
}

// Jitter returns the max number of seconds the job can sleep before actual execution.
func (c *CPUBenchmarkTask) Jitter() uint32 {
	return 0
}

// Run executes the CPU benchmark.
func (c *CPUBenchmarkTask) Run(ctx context.Context) (interface{}, error) {
	cmd := c.execWrapper.CommandContext(ctx, "cpubench", "-j")
	cpubenchOut, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to execute cpubench command: %w", err)
	}
	cpuBenchmarkResult := CPUBenchmarkResult{}
	err = json.Unmarshal(cpubenchOut, &cpuBenchmarkResult)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cpubench output: %w", err)
	}
	client := perf.MustGetZbusClient(ctx)
	statistics := stubs.NewStatisticsStub(client)

	workloads, err := statistics.Workloads(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get workloads number: %w", err)
	}

	cpuBenchmarkResult.Workloads = workloads
	return cpuBenchmarkResult, nil
}
