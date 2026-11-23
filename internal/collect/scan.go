package collect

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	ipmiclient "github.com/bougou/go-ipmi/pkg/client"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// ProbeTimeout bounds a single quick protocol probe during a batch scan.
const ProbeTimeout = 6 * time.Second

// Probe checks whether a host answers the configured management protocol
// before the scanner performs a full collection. It returns an error when the
// protocol is not detected.
func Probe(ctx context.Context, target model.Target) error {
	switch target.Protocol {
	case model.ProtocolSNMP:
		return probeSNMP(target)
	case model.ProtocolIPMI:
		return probeIPMI(ctx, target)
	case model.ProtocolRedfish:
		port := target.Port
		if port == 0 {
			port = 443
		}
		return probeTCP(ctx, targetHost(target.Address), port)
	default:
		return fmt.Errorf("unsupported protocol %q", target.Protocol)
	}
}

// probeSNMP sends one cheap GET with a short timeout instead of the full
// collection, so a mostly empty subnet does not wait for SNMP retries.
func probeSNMP(target model.Target) error {
	params, err := buildSNMPParamsFor(targetHost(target.Address), target.Port, target.SNMP)
	if err != nil {
		return err
	}
	params.Timeout = time.Second
	params.Retries = 0
	if err := params.Connect(); err != nil {
		return err
	}
	defer func() {
		if params.Conn != nil {
			_ = params.Conn.Close()
		}
	}()
	_, err = snmpGetOne(params, oidSysName)
	return err
}

// probeIPMI performs a short session handshake. A successful handshake also
// validates the supplied credentials.
func probeIPMI(ctx context.Context, target model.Target) error {
	port := target.Port
	if port == 0 {
		port = 623
	}
	client, err := ipmiclient.NewClient(target.Address, port, target.IPMI.Username, target.IPMI.Password)
	if err != nil {
		return err
	}
	if strings.EqualFold(target.IPMI.Interface, "lan") {
		client.WithInterface(ipmiclient.InterfaceLan)
	}
	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	if err := client.Connect(probeCtx); err != nil {
		return err
	}
	return client.Close(probeCtx)
}

// probeTCP reports whether a TCP endpoint accepts a connection.
func probeTCP(ctx context.Context, host string, port int) error {
	if host == "" {
		return fmt.Errorf("empty host")
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return conn.Close()
}

// targetHost extracts a bare host from an address that may be a URL or
// contain a port.
func targetHost(address string) string {
	address = strings.TrimSpace(address)
	if strings.Contains(address, "://") {
		if u, err := url.Parse(address); err == nil && u.Hostname() != "" {
			address = u.Hostname()
		}
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	return strings.Trim(address, "[]")
}