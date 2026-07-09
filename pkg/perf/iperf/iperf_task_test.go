package iperf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	execwrapper "github.com/threefoldtech/zos_base/pkg/perf/exec_wrapper"
	"go.uber.org/mock/gomock"
)

func TestIperfTest_Run_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockExec := execwrapper.NewMockExecWrapper(ctrl)
	mockCmd := execwrapper.NewMockExecCmd(ctrl)

	// Create mock HTTP server - need to create raw JSON with IP/HOST and PORT fields
	serversJSON := []byte(`[{"IP/HOST":"192.168.1.100","PORT":"5201"}]`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(serversJSON)
	}))
	defer server.Close()

	task := &IperfTest{
		execWrapper:           mockExec,
		httpClient:            server.Client(),
		serversURL:            server.URL,
		skipReachabilityCheck: true,
	}

	mockExec.EXPECT().
		LookPath("iperf").
		Return("/usr/bin/iperf", nil)

	tcpOutput := createMockIperfOutput(false, 1000000, 2000000)
	udpOutput := createMockIperfOutput(true, 500000, 800000)

	tcpOutputBytes, _ := json.Marshal(tcpOutput)
	udpOutputBytes, _ := json.Marshal(udpOutput)

	mockExec.EXPECT().
		CommandContext(gomock.Any(), "iperf", gomock.Any()).
		Return(mockCmd).
		Times(2)

	mockCmd.EXPECT().CombinedOutput().Return(tcpOutputBytes, nil)
	mockCmd.EXPECT().CombinedOutput().Return(udpOutputBytes, nil)

	ctx := context.Background()
	result, err := task.Run(ctx)

	assert.NoError(t, err)
	assert.NotNil(t, result)

	results, ok := result.([]IperfResult)
	assert.True(t, ok)
	assert.Len(t, results, 2)

	firstResult := results[0]
	assert.Equal(t, "192.168.1.100", firstResult.ServerHost)
	assert.Equal(t, "192.168.1.100", firstResult.ServerIP)
	assert.Equal(t, 5201, firstResult.ServerPort)
	assert.Equal(t, "tcp", firstResult.TestType)
	assert.Equal(t, float64(1000000), firstResult.UploadSpeed)
	assert.Equal(t, float64(2000000), firstResult.DownloadSpeed)
}

func TestIperfTest_Run_HTTPError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockExec := execwrapper.NewMockExecWrapper(ctrl)

	// Create mock HTTP server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	task := &IperfTest{
		execWrapper:           mockExec,
		httpClient:            server.Client(),
		serversURL:            server.URL,
		skipReachabilityCheck: true,
	}

	mockExec.EXPECT().
		LookPath("iperf").
		Return("/usr/bin/iperf", nil)

	// Execute the test
	ctx := context.Background()
	result, err := task.Run(ctx)

	// Verify error
	assert.Error(t, err)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch public iperf3 server")
	assert.Nil(t, result)
}

func TestIperfTest_Run_Iperf3NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockExec := execwrapper.NewMockExecWrapper(ctrl)

	task := &IperfTest{
		execWrapper: mockExec,
	}

	mockExec.EXPECT().
		LookPath("iperf").
		Return("", errors.New("executable file not found in $PATH"))

	ctx := context.Background()
	result, err := task.Run(ctx)

	assert.Error(t, err)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "iperf not found")
	assert.Nil(t, result)
}

func TestIperfTest_Run_NoServersAvailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockExec := execwrapper.NewMockExecWrapper(ctrl)

	// Create mock HTTP server that returns empty list
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	task := &IperfTest{
		execWrapper:           mockExec,
		httpClient:            server.Client(),
		serversURL:            server.URL,
		skipReachabilityCheck: true,
	}

	mockExec.EXPECT().
		LookPath("iperf").
		Return("/usr/bin/iperf", nil)

	ctx := context.Background()
	result, err := task.Run(ctx)

	assert.Error(t, err)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no iperf3 servers available")
	assert.Nil(t, result)
}

func TestNewTask(t *testing.T) {
	task := NewTask()
	assert.NotNil(t, task)
	assert.Equal(t, "iperf", task.ID())
}

// Helper function to create mock iperf output
func createMockIperfOutput(isUDP bool, uploadSpeed, downloadSpeed float64) iperfCommandOutput {
	output := iperfCommandOutput{
		End: End{
			SumSent: Sum{
				BitsPerSecond: uploadSpeed,
			},
			SumReceived: Sum{
				BitsPerSecond: downloadSpeed,
			},
			CPUUtilizationPercent: CPUUtilizationPercent{
				HostTotal:   10.5,
				RemoteTotal: 8.2,
			},
		},
	}

	if isUDP {
		output.End.Streams = []EndStream{
			{
				UDP: UDPSum{
					BitsPerSecond: downloadSpeed,
				},
			},
		}
	}

	return output
}
