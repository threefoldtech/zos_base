package vm

import (
	"fmt"

	"github.com/rs/zerolog"
	"github.com/threefoldtech/zos_base/pkg"
)

func logInterfaceDetails(logEvent *zerolog.Event, interfaces []Interface, networkInfo *pkg.VMNetworkInfo) *zerolog.Event {
	for idx, ifc := range interfaces {
		logEvent = logEvent.
			Str(fmt.Sprintf("interface-%d-id", idx), ifc.ID).
			Str(fmt.Sprintf("interface-%d-tap", idx), ifc.Tap).
			Str(fmt.Sprintf("interface-%d-mac", idx), ifc.Mac)

		if networkInfo != nil && idx < len(networkInfo.Ifaces) {
			vmIface := networkInfo.Ifaces[idx]

			for ipIdx, ip := range vmIface.IPs {
				logEvent = logEvent.Str(fmt.Sprintf("interface-%d-ip-%d", idx, ipIdx), ip.String())
			}

			if vmIface.IP4DefaultGateway != nil {
				logEvent = logEvent.Str(fmt.Sprintf("interface-%d-ipv4-gateway", idx), vmIface.IP4DefaultGateway.String())
			}
			if vmIface.IP6DefaultGateway != nil {
				logEvent = logEvent.Str(fmt.Sprintf("interface-%d-ipv6-gateway", idx), vmIface.IP6DefaultGateway.String())
			}

			if vmIface.PublicIPv4 {
				logEvent = logEvent.Bool(fmt.Sprintf("interface-%d-public-ipv4", idx), true)
			}
			if vmIface.PublicIPv6 {
				logEvent = logEvent.Bool(fmt.Sprintf("interface-%d-public-ipv6", idx), true)
			}

			if vmIface.NetID != "" {
				logEvent = logEvent.Str(fmt.Sprintf("interface-%d-network-id", idx), string(vmIface.NetID))
			}
		}
	}
	return logEvent
}
