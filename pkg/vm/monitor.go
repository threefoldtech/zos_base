package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zosbase/pkg/gridtypes"
	"github.com/threefoldtech/zosbase/pkg/rotate"
	"github.com/threefoldtech/zosbase/pkg/stubs"
)

const (
	failuresBeforeDestroy = 4
	monitorEvery          = 10 * time.Second
	logrotateEvery        = 10 * time.Minute
	cleanupEvery          = 10 * time.Minute
	// cooldownPeriod is the time to wait after a burst of failures before retrying
	cooldownPeriod = 6 * time.Hour
)

var (
	// if the failures marker is set to permanent it means
	// the monitoring will not try to restart this machine
	// when it detects that it is down.
	permanent = struct{}{}

	rotator = rotate.NewRotator(
		rotate.MaxSize(8*rotate.Megabytes),
		rotate.TailSize(4*rotate.Megabytes),
	)
)

// vmFailureState tracks the failure count and cooldown state for a VM
type vmFailureState struct {
	Count           int
	CooldownUntil   time.Time
	LastCooldownLog time.Time
}

func (m *Module) logrotate(ctx context.Context) error {
	log.Debug().Msg("running log rotations for vms")
	running, err := FindAll()
	if err != nil {
		return err
	}

	names := make([]string, 0, len(running))
	for name := range running {
		names = append(names, name)
	}

	return rotator.RotateAll(filepath.Join(m.root, logsDir), names...)
}

// Monitor start vms  monitoring
func (m *Module) Monitor(ctx context.Context) {

	go func() {
		monTicker := time.NewTicker(monitorEvery)
		defer monTicker.Stop()
		logTicker := time.NewTicker(logrotateEvery)
		defer logTicker.Stop()
		cleanupTicker := time.NewTicker(cleanupEvery)
		defer cleanupTicker.Stop()

		for {
			select {
			case <-monTicker.C:
				if err := m.monitor(ctx); err != nil {
					log.Error().Err(err).Msg("failed to run monitoring")
				}
			case <-logTicker.C:
				if err := m.logrotate(ctx); err != nil {
					log.Error().Err(err).Msg("failed to run log rotation")
				}
			case <-cleanupTicker.C:
				if err := m.cleanupCidata(); err != nil {
					log.Error().Err(err).Msg("failed to run cleanup")
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *Module) monitor(ctx context.Context) error {
	// this lock works with Run call to avoid
	// monitoring trying to restart a machine that is not running yet.
	m.lock.Lock()
	defer m.lock.Unlock()
	// list all machines available under `{root}/firecracker`
	items, err := os.ReadDir(m.cfg)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	running, err := FindAll()
	if err != nil {
		return err
	}

	for _, item := range items {
		if item.IsDir() {
			continue
		}

		id := item.Name()

		if err := m.monitorID(ctx, running, id); err != nil {
			log.Err(err).Str("id", id).Msg("failed to monitor machine")
		}

		// remove vm from running vms
		delete(running, id)
	}

	// now we have running vms that shouldn't be running
	// because they have no config.
	for id, ps := range running {
		log.Info().Str("id", id).Msg("machine is running but not configured")
		_ = syscall.Kill(ps.Pid, syscall.SIGKILL)
	}

	return nil
}

func (m *Module) cleanupCidata() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	log.Debug().Msg("running cleanup for vms cidata")

	running, err := FindAll()
	if err != nil {
		return err
	}

	dir := filepath.Join(m.root, cloudInitDir)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to list directory '%s' files: %w", dir, err)
	}

	for _, file := range files {
		name := file.Name()
		if _, ok := running[name]; !ok {
			// Check if VM is being monitored/retried before deleting
			marker, exists := m.failures.Get(name)
			if exists && marker != permanent {
				// VM is in failure tracking (cooldown/retry), don't delete its cloud-init
				log.Debug().Str("vm-id", name).Msg("skipping cloud-init cleanup for VM in retry state")
				continue
			}
			log.Debug().Str("vm-id", name).Msg("removing cloud-init for non-running VM")
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
	}
	return nil
}

func (m *Module) monitorID(ctx context.Context, running map[string]Process, id string) error {
	stub := stubs.NewProvisionStub(m.client)
	log := log.With().Str("id", id).Logger()

	// skip healthcheck vms
	if strings.HasPrefix(id, "healthcheck-vm") {
		return nil
	}
	if ps, ok := running[id]; ok {
		workloadState, exists, err := stub.GetWorkloadStatus(ctx, id)
		if err != nil {
			return errors.Wrapf(err, "failed to get workload status for vm:%s ", id)
		}
		if !exists || workloadState.IsAny(gridtypes.StateDeleted) {
			log.Debug().Str("name", id).Msg("deleting running vm with no active workload")
			m.removeConfig(id)
			_ = syscall.Kill(ps.Pid, syscall.SIGKILL)
		}
		return nil
	}

	// otherwise machine is not running. we need to check if we need to restart
	// it

	marker, ok := m.failures.Get(id)
	if !ok {
		// no previous value. so this is the first failure
		m.failures.Set(id, &vmFailureState{Count: 0, CooldownUntil: time.Time{}, LastCooldownLog: time.Time{}}, cache.DefaultExpiration)
		marker, _ = m.failures.Get(id)
	}

	if marker == permanent {
		// if the marker is permanent. it means that this vm
		// is being deleted or not monitored. we don't need to take any action here
		// (don't try to restart or delete)
		m.removeConfig(id)
		return nil
	}

	// Check if marker is a vmFailureState or old int format
	var state *vmFailureState
	switch v := marker.(type) {
	case *vmFailureState:
		state = v
	case int:
		// Migrate old int format to new struct format
		state = &vmFailureState{Count: v, CooldownUntil: time.Time{}, LastCooldownLog: time.Time{}}
		m.failures.Set(id, state, cache.DefaultExpiration)
	default:
		// Unknown format, reset
		state = &vmFailureState{Count: 0, CooldownUntil: time.Time{}, LastCooldownLog: time.Time{}}
		m.failures.Set(id, state, cache.DefaultExpiration)
	}

	// Check if we're in cooldown period
	now := time.Now()
	if !state.CooldownUntil.IsZero() && now.Before(state.CooldownUntil) {
		// Only log cooldown message every 30 minutes
		if state.LastCooldownLog.IsZero() || now.Sub(state.LastCooldownLog) >= 30*time.Minute {
			log.Debug().Str("name", id).
				Time("cooldown_until", state.CooldownUntil).
				Dur("remaining", state.CooldownUntil.Sub(now)).
				Msg("vm in cooldown period, skipping restart attempt")
			state.LastCooldownLog = now
			m.failures.Set(id, state, cache.NoExpiration)
		}
		return nil
	}

	// Set error state before attempting restart (if not in cooldown)
	// This ensures the database reflects the actual state
	log.Info().Str("name", id).Msg("vm detected as down, setting state to error")
	log.Debug().Str("workload-id", id).Msg("attempting to set vm state to error")
	if err := stub.SetWorkloadError(ctx, id, "vm detected as down"); err != nil {
		// Check if deployment actually doesn't exist
		if strings.Contains(err.Error(), "deployment does not exist") {
			// Verify by checking workload status
			_, exists, checkErr := stub.GetWorkloadStatus(ctx, id)
			if checkErr == nil && !exists {
				log.Warn().Str("workload-id", id).Msg("vm deployment confirmed deleted, stopping monitoring")
				// Set permanent marker to prevent further restart attempts
				m.failures.Set(id, permanent, cache.NoExpiration)
				return nil
			}
			// If we can't confirm or it exists, treat as transient error and continue
			log.Warn().Err(err).Str("workload-id", id).Msg("failed to set vm error state, but continuing retry")
		} else {
			log.Error().Err(err).Str("workload-id", id).Msg("failed to set vm error state")
		}
	}

	// If we just exited cooldown, reset the failure count for a new burst
	if !state.CooldownUntil.IsZero() && now.After(state.CooldownUntil) {
		log.Info().Str("name", id).Msg("cooldown period expired, resetting failure count for new burst")
		state.Count = 0
		state.CooldownUntil = time.Time{}
		state.LastCooldownLog = time.Time{} // Reset cooldown log timer
		// Go back to using DefaultExpiration after cooldown expires
		m.failures.Set(id, state, cache.DefaultExpiration)
	}

	// Increment failure count
	state.Count++
	m.failures.Set(id, state, cache.DefaultExpiration)

	var reason error
	if state.Count < failuresBeforeDestroy {
		vm, err := MachineFromFile(m.configPath(id))

		if err != nil {
			return err
		}

		if vm.NoKeepAlive {
			// if the permanent marker was not set, and we reach here it's possible that
			// the vmd was restarted, hence the in-memory copy of this flag was gone. Hence
			// we need to set it correctly, and just return
			m.failures.Set(id, permanent, cache.NoExpiration)
			return nil
		}

		log.Debug().Str("name", id).Int("attempt", state.Count).Msg("trying to restart the vm")
		if _, err = vm.Run(ctx, m.socketPath(id), m.logsPath(id)); err != nil {
			reason = m.withLogs(m.logsPath(id), err)
			log.Warn().Err(reason).Str("name", id).Int("failures", state.Count).Msg("vm restart failed")
		} else {
			// Success! Reset failure count and set state to OK
			log.Info().Str("name", id).Msg("vm restarted successfully")
			state.Count = 0
			state.CooldownUntil = time.Time{}
			m.failures.Set(id, state, cache.DefaultExpiration)

			// Update workload state to OK since VM is running
			log.Debug().Str("workload-id", id).Msg("attempting to set vm state to ok")
			if err := stub.SetWorkloadOk(ctx, id); err != nil {
				// Check if deployment actually doesn't exist
				if strings.Contains(err.Error(), "deployment does not exist") {
					// Verify by checking workload status
					_, exists, checkErr := stub.GetWorkloadStatus(ctx, id)
					if checkErr == nil && !exists {
						log.Warn().Str("workload-id", id).Msg("vm deployment confirmed deleted, will be stopped on next monitor cycle")
						// Set permanent marker - next monitor cycle will kill VM and clean up config
						m.failures.Set(id, permanent, cache.NoExpiration)
						return nil
					}
					// If we can't confirm or it exists, treat as transient error and continue
					log.Warn().Err(err).Str("workload-id", id).Msg("failed to set vm state to ok, but VM is running")
				} else {
					log.Error().Err(err).Str("workload-id", id).Msg("failed to set vm state to ok")
				}
			}
		}
	} else {
		// Reached failure limit, enter cooldown period instead of decommissioning
		state.CooldownUntil = now.Add(cooldownPeriod)
		// Use NoExpiration to ensure cooldown state persists beyond cache default expiration
		m.failures.Set(id, state, cache.NoExpiration)
		log.Warn().Str("name", id).
			Int("failures", state.Count).
			Time("cooldown_until", state.CooldownUntil).
			Dur("cooldown_duration", cooldownPeriod).
			Msg("vm reached failure limit, entering cooldown period before next retry burst")
		// Do NOT call DecommissionCached or removeConfig - keep the VM for retry
	}

	return nil
}
