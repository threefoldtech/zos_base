package perf

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/threefoldtech/zbus"
	"github.com/threefoldtech/zos_base/pkg/mocks"
	"go.uber.org/mock/gomock"
)

func TestWithZbusClient(t *testing.T) {
	t.Run("adds client to context", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := mocks.NewMockClient(ctrl)
		ctx := WithZbusClient(context.Background(), mockClient)

		retrievedClient, ok := ctx.Value(zbusClientKey{}).(zbus.Client)
		assert.True(t, ok)
		assert.Equal(t, mockClient, retrievedClient)
	})
}

func TestMustGetZbusClient(t *testing.T) {
	t.Run("successfully retrieves client", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := mocks.NewMockClient(ctrl)
		ctx := WithZbusClient(context.Background(), mockClient)

		retrievedClient := MustGetZbusClient(ctx)
		assert.Equal(t, mockClient, retrievedClient)
	})

	t.Run("panics when client not in context", func(t *testing.T) {
		ctx := context.Background()

		assert.Panics(t, func() {
			MustGetZbusClient(ctx)
		})
	})

	t.Run("panics when context value is wrong type", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), zbusClientKey{}, "not a client")
		assert.Panics(t, func() {
			MustGetZbusClient(ctx)
		})
	})
}

func TestTryGetZbusClient(t *testing.T) {

	t.Run("successfully retrieves client", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := mocks.NewMockClient(ctrl)
		ctx := WithZbusClient(context.Background(), mockClient)

		retrievedClient, err := TryGetZbusClient(ctx)
		require.NoError(t, err)
		assert.Equal(t, mockClient, retrievedClient)
	})

	t.Run("returns error when client not in context", func(t *testing.T) {
		ctx := context.Background()

		retrievedClient, err := TryGetZbusClient(ctx)
		require.Error(t, err)
		assert.Nil(t, retrievedClient)
		assert.Contains(t, err.Error(), "context does not have zbus client")
	})

	t.Run("returns error when context value is wrong type", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), zbusClientKey{}, "not a client")

		retrievedClient, err := TryGetZbusClient(ctx)
		require.Error(t, err)
		assert.Nil(t, retrievedClient)
		assert.Contains(t, err.Error(), "context does not have zbus client")
	})
}
