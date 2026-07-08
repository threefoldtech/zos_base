package iperf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg/perf"
	execwrapper "github.com/threefoldtech/zos_base/pkg/perf/exec_wrapper"
)

const (
	maxRetries      = 3
	initialInterval = 10 * time.Second
	maxInterval     = 90 * time.Second
	maxElapsedTime  = 7 * time.Minute
	iperfTimeout    = 90 * time.Second

	iperf3ServersURL = "https://export.iperf3serverlist.net/listed_iperf3_servers.json"
)

// IperfTest for iperf tcp/udp tests
type IperfTest struct {
	// Optional dependencies for testing
	execWrapper           execwrapper.ExecWrapper
	httpClient            *http.Client
	serversURL            string // for testing override
	skipReachabilityCheck bool   // for testing - skip server reachability check
}

// IperfResult for iperf test results
type IperfResult struct {
	UploadSpeed   float64               `json:"upload_speed"`   // in bit/sec
	DownloadSpeed float64               `json:"download_speed"` // in bit/sec
	ServerHost    string                `json:"server_host"`
	ServerIP      string                `json:"server_ip"`
	ServerPort    int                   `json:"server_port"`
	TestType      string                `json:"test_type"`
	Error         string                `json:"error"`
	CpuReport     CPUUtilizationPercent `json:"cpu_report"`
}

// Iperf3Server represents a public iperf3 server from the list
type Iperf3Server struct {
	Host    string `json:"IP/HOST"` // IP or hostname
	Port    int    `json:"-"`       // Not directly unmarshaled
	PortStr string `json:"PORT"`    // Port comes as string in JSON
}

// UnmarshalJSON custom unmarshaler to handle port as string or port range
func (s *Iperf3Server) UnmarshalJSON(data []byte) error {
	type Alias Iperf3Server
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(s),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Convert port string to int, handling ranges like "9201-9240"
	if s.PortStr != "" {
		portStr := strings.Split(s.PortStr, "-")[0] // Take first port if range
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("invalid port value: %s", s.PortStr)
		}
		s.Port = port
	}

	return nil
}

// NewTask creates a new iperf test
func NewTask() perf.Task {
	// because go-iperf left tmp directories with perf binary in it each time
	// the task had run
	matches, _ := filepath.Glob("/tmp/goiperf*")
	for _, match := range matches {
		os.RemoveAll(match)
	}
	return &IperfTest{}
}

// ID returns the ID of the tcp task
func (t *IperfTest) ID() string {
	return "iperf"
}

// Cron returns the schedule for the tcp task
func (t *IperfTest) Cron() string {
	return "0 0 */6 * * *"
}

// Description returns the task description
func (t *IperfTest) Description() string {
	return "Test network performance against public iperf3 servers with both UDP and TCP"
}

// Jitter returns the max number of seconds the job can sleep before actual execution.
func (t *IperfTest) Jitter() uint32 {
	return 20 * 60
}

// Run runs the tcp test and returns the result
func (t *IperfTest) Run(ctx context.Context) (interface{}, error) {
	// Check if iperf is available
	if t.execWrapper != nil {
		execWrap := t.execWrapper
		_, err := execWrap.LookPath("iperf")
		if err != nil {
			return nil, errors.Wrap(err, "iperf not found")
		}
	} else {
		_, err := exec.LookPath("iperf")
		if err != nil {
			return nil, errors.Wrap(err, "iperf not found")
		}
	}

	// Fetch a reachable public iperf3 server
	server, err := t.fetchIperf3Server(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch public iperf3 server")
	}

	if server == nil {
		return nil, errors.New("no public iperf3 server available")
	}

	log.Info().Str("server-host", server.Host).Int("server-port", server.Port).Msg("using iperf3 server for testing")

	var results []IperfResult

	// Run TCP test
	res := t.runIperfTest(ctx, *server, true)
	results = append(results, res)

	// Run UDP test
	res = t.runIperfTest(ctx, *server, false)
	results = append(results, res)

	return results, nil
}

// fetchIperf3Server fetches the list of public iperf3 servers and finds the first reachable one
func (t *IperfTest) fetchIperf3Server(ctx context.Context) (*Iperf3Server, error) {
	client := t.httpClient
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	url := t.serversURL
	if url == "" {
		url = iperf3ServersURL
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch iperf3 servers")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}

	var servers []Iperf3Server
	if err := json.Unmarshal(body, &servers); err != nil {
		return nil, errors.Wrap(err, "failed to parse iperf3 servers list")
	}

	log.Info().Int("count", len(servers)).Msg("fetched public iperf3 servers")

	// For testing, skip reachability check
	if t.skipReachabilityCheck {
		if len(servers) == 0 {
			return nil, errors.New("no iperf3 servers available")
		}
		return &servers[0], nil
	}

	// Find first reachable server by shuffling and checking
	reachableServer := t.findFirstReachableServer(ctx, servers)
	if reachableServer == nil {
		return nil, errors.New("no reachable iperf3 servers found")
	}

	log.Info().Str("host", reachableServer.Host).Int("port", reachableServer.Port).Msg("found reachable iperf3 server")

	return reachableServer, nil
}

// findFirstReachableServer shuffles the server list and returns the first reachable one
func (t *IperfTest) findFirstReachableServer(ctx context.Context, servers []Iperf3Server) *Iperf3Server {
	// Shuffle servers to randomize selection
	shuffled := make([]Iperf3Server, len(servers))
	copy(shuffled, servers)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	// Find first reachable server
	for _, server := range shuffled {
		if t.isServerReachable(ctx, server) {
			return &server
		}
		log.Debug().Str("host", server.Host).Int("port", server.Port).Msg("iperf3 server unreachable, trying next")
	}

	return nil
}

// isServerReachable checks if a server is reachable by attempting a TCP connection
func (t *IperfTest) isServerReachable(ctx context.Context, server Iperf3Server) bool {
	// Skip servers with no host/IP or invalid port
	if server.Host == "" || server.Port == 0 {
		return false
	}

	address := fmt.Sprintf("%s:%d", server.Host, server.Port)

	// Use a short timeout for connectivity check
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	defer conn.Close()

	return true
}

func (t *IperfTest) runIperfTest(ctx context.Context, server Iperf3Server, tcp bool) IperfResult {
	opts := make([]string, 0)

	opts = append(opts,
		"--client", server.Host,
		"--port", fmt.Sprint(server.Port),
		"--time", "10", // 10 second test duration
		"--json",
	)

	if !tcp {
		opts = append(opts, "--udp", "--bandwidth", "10M") // 10 Mbps for UDP
	}

	var execWrap execwrapper.ExecWrapper = &execwrapper.RealExecWrapper{}
	if t.execWrapper != nil {
		execWrap = t.execWrapper
	}

	var report iperfCommandOutput
	operation := func() error {
		timeoutCtx, cancel := context.WithTimeout(ctx, iperfTimeout)
		defer cancel()

		res := runIperf3Command(timeoutCtx, opts, execWrap)
		if res.Error != "" {
			return errors.New(res.Error)
		}

		report = res
		return nil
	}

	notify := func(err error, waitTime time.Duration) {
		log.Debug().Err(err).Stringer("retry-in", waitTime).Msg("retrying iperf3 test")
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = initialInterval
	bo.MaxInterval = maxInterval
	bo.MaxElapsedTime = maxElapsedTime

	b := backoff.WithMaxRetries(bo, maxRetries)
	err := backoff.RetryNotify(operation, b, notify)

	proto := "tcp"
	if !tcp {
		proto = "udp"
	}

	iperfResult := IperfResult{
		ServerHost: server.Host,
		ServerIP:   server.Host,
		ServerPort: server.Port,
		TestType:   proto,
	}

	if err != nil {
		log.Error().Err(err).Str("server", server.Host).Str("type", proto).Msg("iperf3 test failed")
		iperfResult.Error = err.Error()
		return iperfResult
	}

	iperfResult.CpuReport = report.End.CPUUtilizationPercent
	iperfResult.Error = report.Error

	// Both TCP and UDP use sum_sent and sum_received in the end section
	iperfResult.UploadSpeed = report.End.SumSent.BitsPerSecond
	iperfResult.DownloadSpeed = report.End.SumReceived.BitsPerSecond

	// Log if there's an error in the report
	if report.Error != "" {
		log.Warn().Str("server", server.Host).Str("type", proto).Str("iperf-error", report.Error).Msg("iperf3 test completed with error")
	}

	log.Info().Str("server", server.Host).Str("type", proto).Float64("upload-mbps", iperfResult.UploadSpeed/1000000).Float64("download-mbps", iperfResult.DownloadSpeed/1000000).Msg("iperf3 test completed")

	return iperfResult
}

func runIperf3Command(ctx context.Context, opts []string, execWrap execwrapper.ExecWrapper) iperfCommandOutput {
	output, err := execWrap.CommandContext(ctx, "iperf", opts...).CombinedOutput()
	exitErr := &exec.ExitError{}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Warn().Str("target", opts[1]).Msg("iperf3 command timed out")
		}
		if !errors.As(err, &exitErr) {
			log.Error().Err(err).Msg("failed to run iperf3")
		}

		return iperfCommandOutput{}
	}
	var report iperfCommandOutput
	if err := json.Unmarshal(output, &report); err != nil {
		log.Error().Err(err).Str("output", string(output)).Msg("failed to parse iperf3 output")
		return iperfCommandOutput{}
	}

	return report
}
