package nft

import (
	"fmt"
	"io"
	"os/exec"

	_ "embed"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/threefoldtech/zos_base/pkg/netlight/namespace"
	"github.com/vishvananda/netlink"
)

//go:embed lansecurity.tmpl
var lanSecurityTmpl string

// Apply applies the ntf configuration contained in the reader r
// if ns is specified, the nft command is execute in the network namespace names ns
func Apply(r io.Reader, ns string) error {
	var cmd *exec.Cmd

	if ns != "" {
		cmd = exec.Command("ip", "netns", "exec", ns, "nft", "-f", "-")
	} else {
		cmd = exec.Command("nft", "-f", "-")
	}

	cmd.Stdin = r

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Str("output", string(out)).Msg("error during nft")
		if eerr, ok := err.(*exec.ExitError); ok {
			return errors.Wrapf(err, "failed to execute nft: %v", string(eerr.Stderr))
		}
		return errors.Wrap(err, "failed to execute nft")
	}
	return nil
}

// DropTrafficToLAN drops all the outgoing traffic to any peers on
// the same lan network, but allow dicovery port for ygg/myc by accepting
// traffic to/from dest/src ports.
func DropTrafficToLAN(netns string) error {
	var dgw netlink.Neigh

	toRun := func(_ ns.NetNS) error {
		var err error
		dgw, err = getDefaultGW()
		return err
	}

	if netns != "" {
		nss, err := namespace.GetByName(netns)
		if err != nil {
			return fmt.Errorf("failed to get namespace %q", netns)
		}
		defer nss.Close()

		if err := nss.Do(toRun); err != nil {
			return fmt.Errorf("failed to execute in namespace %q: %w", netns, err)
		}
	} else {
		if err := toRun(nil); err != nil {
			return fmt.Errorf("failed to get default gateway: %w", err)
		}
	}

	if !dgw.IP.IsPrivate() {
		log.Warn().Msg("skip LAN security. default gateway is public")
		return nil
	}

	rules, err := renderRulesTemplate(lanSecurityTmpl, dgw)
	if err != nil {
		return fmt.Errorf("failed to render nft rules template: %w", err)
	}

	return Apply(rules, netns)
}
