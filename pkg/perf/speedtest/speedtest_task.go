package speedtest

import (
	"context"
	"fmt"
	"math"

	"github.com/rs/zerolog/log"
	"github.com/showwin/speedtest-go/speedtest"
	"github.com/threefoldtech/zos_base/pkg/perf"
)

type SpeedTestTask struct {
}

// CPUBenchmarkResult holds CPU benchmark results with the workloads number during the benchmark.
type SpeedTestResults struct {
	Download float64 `json:"download_speed"`
	Upload   float64 `json:"upload_speed"`
}

var _ perf.Task = (*SpeedTestTask)(nil)

// ID returns task ID.
func (c *SpeedTestTask) ID() string {
	return "speedtest"
}

// Cron returns task cron schedule.
func (c *SpeedTestTask) Cron() string {
	return "*/30 * * * * *"
}

// Description returns task description.
func (c *SpeedTestTask) Description() string {
	return "Measures the download/upload speed of the node."
}

// Jitter returns the max number of seconds the job can sleep before actual execution.
func (c *SpeedTestTask) Jitter() uint32 {
	return 0
}

func NewTask() perf.Task {
	return &SpeedTestTask{}
}

// Run executes the SpeedTest task.
func (c *SpeedTestTask) Run(ctx context.Context) (interface{}, error) {
	serverList, err := speedtest.FetchServers()
	if err != nil {
		return nil, err
	}
	servers, err := serverList.FindServer([]int{})
	if err != nil {
		return nil, err
	}
	if len(servers) < 1 {
		return nil, fmt.Errorf("no speedtest server found")
	}

	speedtestServer := servers[0]

	err = speedtestServer.DownloadTest()
	if err != nil {
		log.Error().Err(err).Msg("speedtest download test failed")
		return nil, err
	}

	err = speedtestServer.UploadTest()
	if err != nil {
		log.Error().Err(err).Msg("speedtest upload test failed")
		return nil, err
	}

	download := speedtestServer.DLSpeed.Mbps()
	upload := speedtestServer.ULSpeed.Mbps()

	if math.IsNaN(download) || math.IsNaN(upload) {
		return nil, fmt.Errorf("speedtest returned NaN value")
	}

	log.Info().Msgf("speedtest result: download %.2f Mbps, upload %.2f Mbps", download, upload)
	return SpeedTestResults{
		Download: download,
		Upload:   upload,
	}, nil

}
