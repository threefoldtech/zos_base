package diagnostics

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/require"
	"github.com/threefoldtech/zbus"
	"github.com/threefoldtech/zos_base/pkg/mocks"
	"go.uber.org/mock/gomock"
)

type mockRedisPool struct {
	conn redis.Conn
}

func (m *mockRedisPool) Get() redis.Conn {
	return m.conn
}

type redisKeyValue struct {
	Result map[string][]string `json:"result"`
}

func TestGetSystemDiagnostics(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockZbus := mocks.NewMockClient(ctrl)
	mockRedis := mocks.NewMockConn(ctrl)

	pool := &mockRedisPool{conn: mockRedis}

	manager := &DiagnosticsManager{
		redisPool:  pool,
		zbusClient: mockZbus,
	}

	t.Run("success scenario", func(t *testing.T) {
		ctx := context.Background()

		status := zbus.Status{
			Objects: []zbus.ObjectID{{Name: "test", Version: "1.0"}},
			Workers: []zbus.WorkerStatus{{
				State:     "free",
				StartTime: time.Now(),
				Action:    "test",
			}},
		}

		for _, module := range Modules {
			mockZbus.EXPECT().
				Status(gomock.Any(), module).
				Return(status, nil)
		}

		healthyResponse := map[string][]string{"test": {}}
		healthyBytes, _ := json.Marshal(redisKeyValue{Result: healthyResponse})

		mockRedis.EXPECT().
			Do("GET", testNetworkKey).
			Return(healthyBytes, nil)

		mockRedis.EXPECT().
			Close().
			Return(nil)

		diagnostics, err := manager.GetSystemDiagnostics(ctx)

		require.NoError(t, err)
		require.True(t, diagnostics.SystemStatusOk)
		require.True(t, diagnostics.Healthy)
		require.Len(t, diagnostics.ZosModules, len(Modules))
	})
}

func TestIsHealthy(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRedis := mocks.NewMockConn(ctrl)
	pool := &mockRedisPool{conn: mockRedis}

	manager := &DiagnosticsManager{
		redisPool: pool,
	}

	tests := []struct {
		name     string
		setup    func()
		expected bool
	}{
		{
			name: "healthy system",
			setup: func() {
				response := map[string][]string{"test": {}}
				bytes, _ := json.Marshal(redisKeyValue{Result: response})
				mockRedis.EXPECT().
					Do("GET", testNetworkKey).
					Return(bytes, nil)
				mockRedis.EXPECT().
					Close().
					Return(nil)
			},
			expected: true,
		},
		{
			name: "unhealthy system",
			setup: func() {
				response := map[string][]string{"test": {"error"}}
				bytes, _ := json.Marshal(redisKeyValue{Result: response})
				mockRedis.EXPECT().
					Do("GET", testNetworkKey).
					Return(bytes, nil)
				mockRedis.EXPECT().
					Close().
					Return(nil)
			},
			expected: false,
		},
		{
			name: "redis error",
			setup: func() {
				mockRedis.EXPECT().
					Do("GET", testNetworkKey).
					Return(nil, redis.ErrNil)
				mockRedis.EXPECT().
					Close().
					Return(nil)
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			result := manager.isHealthy()
			require.Equal(t, tt.expected, result)
		})
	}
}
