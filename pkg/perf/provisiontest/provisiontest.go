package provisiontest

import (
	"context"
	"fmt"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg/perf"
	"github.com/threefoldtech/zos_base/pkg/stubs"
)

const (
	id          = "provisiontest"
	schedule    = "0 0 0 * * *"
	description = "daily provision test that deploys a test VM to verify the node can run virtual machines and sets flags for the power daemon"
)

type provisionTestTask struct{}

var _ perf.Task = (*provisionTestTask)(nil)

func NewTask() perf.Task {
	return &provisionTestTask{}
}

func (t *provisionTestTask) ID() string {
	return id
}

func (t *provisionTestTask) Cron() string {
	return schedule
}

func (t *provisionTestTask) Description() string {
	return description
}

func (t *provisionTestTask) Jitter() uint32 {
	return 30 * 60
}

func (t *provisionTestTask) Run(ctx context.Context) (interface{}, error) {
	log.Debug().Msg("starting provision test task")

	cl := perf.MustGetZbusClient(ctx)
	zui := stubs.NewZUIStub(cl)

	var result []string

	op := func() error {
		errs := vmCheck(ctx)
		result = errorsToStrings(errs)

		if err := zui.PushErrors(ctx, "vm", result); err != nil {
			return err
		}

		if len(errs) != 0 {
			return fmt.Errorf("provision test failed: %s", result)
		}

		return nil
	}

	notify := func(err error, t time.Duration) {
		log.Error().Err(err).Dur("retry-in", t).Msg("failed provision test. retrying")
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 3 * time.Minute
	bo.MaxInterval = 30 * time.Second
	bo.MaxElapsedTime = 10 * time.Minute

	_ = backoff.RetryNotify(op, bo, notify)

	return result, nil
}

func errorsToStrings(errs []error) []string {
	s := make([]string, 0, len(errs))
	for _, err := range errs {
		s = append(s, err.Error())
	}
	return s
}
