package healthcheck

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg/app"
	"github.com/threefoldtech/zos_base/pkg/environment"
)

const defaultRequestTimeout = 10 * time.Second

// function: at least one instance of each service should be reachable
// returns errors as a report for perf healthcheck
// a side effect:  set/delete the not-reachable flag
func networkCheck(ctx context.Context) []error {
	var (
		wg     sync.WaitGroup
		errMu  sync.Mutex
		errors []error
	)

	env := environment.MustGet()
	services := map[string][]string{
		"substrate":  env.SubstrateURL,
		"activation": env.ActivationURL,
		"relay":      env.RelaysURLs,
		"graphql":    env.GraphQL,
		"hub":        {env.FlistURL},
		"kyc":        {env.KycURL},
	}

	for service, instances := range services {
		wg.Add(1)
		go func(service string, instances []string) {
			defer wg.Done()

			if err := verifyAtLeastOneIsReachable(ctx, service, instances); err != nil {
				errMu.Lock()
				errors = append(errors, err)
				errMu.Unlock()
			}

		}(service, instances)
	}

	wg.Wait()

	if len(errors) == 0 {
		log.Debug().Msg("all network checks passed")
		if err := app.DeleteFlag(app.NotReachable); err != nil {
			log.Error().Err(err).Msg("failed to delete not-reachable flag")
		}
	} else {
		log.Warn().Int("failed_checks", len(errors)).Msg("some network checks failed")
		if err := app.SetFlag(app.NotReachable); err != nil {
			log.Error().Err(err).Msg("failed to set not-reachable flag")
		}
	}

	return errors
}

func verifyAtLeastOneIsReachable(ctx context.Context, service string, instances []string) error {
	if len(instances) == 0 {
		return fmt.Errorf("no instances provided for service %s", service)
	}

	var unreachableErrors []string
	for _, instance := range instances {
		if err := checkService(ctx, instance); err == nil {
			return nil
		} else {
			unreachableErrors = append(unreachableErrors, err.Error())
		}
	}

	return fmt.Errorf("all %s instances are unreachable: %s", service, strings.Join(unreachableErrors, "; "))
}

func checkService(ctx context.Context, serviceUrl string) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()

	address, err := parseUrl(serviceUrl)
	if err != nil {
		return fmt.Errorf("invalid URL %s: %w", serviceUrl, err)
	}

	if err := isReachable(timeoutCtx, address); err != nil {
		return fmt.Errorf("%s is not reachable: %w", serviceUrl, err)
	}

	return nil
}

func parseUrl(serviceUrl string) (string, error) {
	u, err := url.Parse(serviceUrl)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}

	if u.Host == "" {
		return "", fmt.Errorf("missing hostname in URL")
	}

	port := ":80"
	if u.Scheme == "https" || u.Scheme == "wss" {
		port = ":443"
	}

	if u.Port() == "" {
		u.Host += port
	}

	return u.Host, nil
}

func isReachable(ctx context.Context, address string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	return nil
}
