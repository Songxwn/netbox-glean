package collect

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/Songxwn/netbox-glean/internal/model"
)

const (
	// pingMaxHosts bounds one sweep so a wide CIDR cannot turn into an
	// unintentional network scan.
	pingMaxHosts    = 1024
	pingConcurrency = 64
	pingTimeout     = 1500 * time.Millisecond

	icmpProtocolIPv4 = 1
	icmpProtocolIPv6 = 58
)

// Sweep probes every usable host in cidrs and returns the addresses that
// answered an ICMP echo request, the number of addresses probed, and any
// warnings. IPv4 and IPv6 are swept separately with ICMP/ICMPv6. Unsupported
// or oversized ranges are skipped. If neither an unprivileged ping socket nor
// a raw ICMP socket can be opened the sweep is skipped.
func Sweep(ctx context.Context, cidrs []string, logf model.LogFunc) ([]string, int, []string) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	v4, v6, warnings := expandScanTargets(cidrs)
	total := len(v4) + len(v6)
	if total == 0 {
		return nil, 0, warnings
	}

	type familyResult struct {
		alive    []string
		warnings []string
	}
	results := make([]familyResult, 2)
	var wg sync.WaitGroup
	if len(v4) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[0].alive, results[0].warnings = sweepFamily(ctx, v4, 4, logf)
		}()
	}
	if len(v6) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[1].alive, results[1].warnings = sweepFamily(ctx, v6, 6, logf)
		}()
	}
	wg.Wait()

	alive := map[string]bool{}
	for _, result := range results {
		warnings = append(warnings, result.warnings...)
		for _, address := range result.alive {
			alive[address] = true
		}
	}
	if len(alive) == 0 {
		return nil, total, warnings
	}
	out := make([]string, 0, len(alive))
	for address := range alive {
		out = append(out, address)
	}
	sort.Slice(out, func(i, j int) bool {
		a := net.ParseIP(out[i])
		b := net.ParseIP(out[j])
		if a == nil || b == nil {
			return out[i] < out[j]
		}
		return bytes.Compare(a.To16(), b.To16()) < 0
	})
	return out, total, warnings
}

// sweepFamily sends echo requests to hosts and collects replies until the
// single receive window closes.
func sweepFamily(ctx context.Context, hosts []net.IP, family int, logf model.LogFunc) ([]string, []string) {
	var warnings []string
	conn, unprivileged, err := openPingConn(family)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("ICMPv%d 扫描不可用（需要 NET_RAW 权限或允许 ping socket）: %v", family, err))
		return nil, warnings
	}
	defer conn.Close()
	logf("ICMPv%d 扫描 %d 个地址", family, len(hosts))

	if err := conn.SetReadDeadline(time.Now().Add(pingTimeout)); err != nil {
		warnings = append(warnings, fmt.Sprintf("ICMPv%d 扫描设置超时失败: %v", family, err))
		return nil, warnings
	}

	var mu sync.Mutex
	alive := map[string]bool{}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 1500)
		failures := 0
		for {
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					return
				}
				if errors.Is(err, net.ErrClosed) {
					return
				}
				failures++
				if failures >= 32 {
					return
				}
				continue
			}
			msg, err := icmp.ParseMessage(icmpProtocol(family), buf[:n])
			if err != nil || msg.Type != echoReplyType(family) {
				continue
			}
			ip := peerIP(peer)
			if ip == nil {
				continue
			}
			mu.Lock()
			alive[ip.String()] = true
			mu.Unlock()
		}
	}()

	id := int(uint16(os.Getpid() & 0xffff))
	var seq uint32
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		jobs := make(chan net.IP)
		var wg sync.WaitGroup
		for i := 0; i < pingConcurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ip := range jobs {
					sendEcho(conn, ip, family, id, int(atomic.AddUint32(&seq, 1)&0xffff), unprivileged)
				}
			}()
		}
		for _, ip := range hosts {
			select {
			case jobs <- ip:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return
			}
		}
		close(jobs)
		wg.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = conn.Close()
		<-readDone
	case <-writeDone:
		<-readDone
	}

	mu.Lock()
	out := make([]string, 0, len(alive))
	for address := range alive {
		out = append(out, address)
	}
	mu.Unlock()
	return out, warnings
}

// expandScanTargets lists the hosts to probe, split by address family.
// Network and broadcast addresses are skipped for IPv4 prefixes up to /30;
// /31 and /32 are probed as-is. IPv6 prefixes wider than /120 are rejected
// because enumerating them is not viable.
func expandScanTargets(cidrs []string) (v4, v6 []net.IP, warnings []string) {
	appendHost := func(list *[]net.IP, ip net.IP) bool {
		if len(v4)+len(v6) >= pingMaxHosts {
			warnings = append(warnings, fmt.Sprintf("单次扫描最多 %d 个地址，剩余网段已忽略", pingMaxHosts))
			return false
		}
		*list = append(*list, ip)
		return true
	}

	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("忽略无效的扫描网段 %q: %v", cidr, err))
			continue
		}
		ones, bits := network.Mask.Size()
		switch bits {
		case 32:
			total := uint64(1) << uint(32-ones)
			first := uint64(binary.BigEndian.Uint32(network.IP.To4()))
			var start, end uint64
			if ones >= 31 {
				start, end = 0, total-1
			} else {
				start, end = 1, total-2
			}
			for offset := start; offset <= end; offset++ {
				var buf [4]byte
				binary.BigEndian.PutUint32(buf[:], uint32(first+offset))
				if !appendHost(&v4, net.IPv4(buf[0], buf[1], buf[2], buf[3])) {
					return v4, v6, warnings
				}
			}
		case 128:
			hostBits := 128 - ones
			maxHostBits := 128 - model.MaxIPv6ScanPrefix
			if hostBits > maxHostBits {
				warnings = append(warnings, fmt.Sprintf(
					"IPv6 扫描网段 %q 过大，最多支持 /%d（%d 个地址）；更宽的网段请使用 SNMP NDP 来源",
					cidr, model.MaxIPv6ScanPrefix, 1<<uint(maxHostBits)))
				continue
			}
			base := network.IP.To16()
			count := uint64(1) << uint(hostBits)
			start := binary.BigEndian.Uint64(base[8:])
			for offset := uint64(0); offset < count; offset++ {
				ip := make(net.IP, net.IPv6len)
				copy(ip, base)
				binary.BigEndian.PutUint64(ip[8:], start+offset)
				if !appendHost(&v6, ip) {
					return v4, v6, warnings
				}
			}
		default:
			warnings = append(warnings, fmt.Sprintf("忽略无效的扫描网段 %q", cidr))
		}
	}
	return v4, v6, warnings
}

// expandScanCIDRs lists the IPv4 hosts to probe. It is kept for callers and
// tests that only deal with the IPv4 sweep.
func expandScanCIDRs(cidrs []string) ([]net.IP, []string) {
	v4, _, warnings := expandScanTargets(cidrs)
	return v4, warnings
}

// openPingConn prefers an unprivileged ping socket (Linux/Darwin) and falls
// back to a raw ICMP socket on platforms where that requires privileges. The
// boolean reports whether the connection is a datagram ping socket, which
// requires UDP style destinations when writing.
func openPingConn(family int) (*icmp.PacketConn, bool, error) {
	network := "udp4"
	address := "0.0.0.0"
	rawNetwork := "ip4:icmp"
	if family == 6 {
		network, address, rawNetwork = "udp6", "::", "ip6:ipv6-icmp"
	}
	if conn, err := icmp.ListenPacket(network, address); err == nil {
		return conn, true, nil
	} else if raw, rawErr := icmp.ListenPacket(rawNetwork, address); rawErr == nil {
		return raw, false, nil
	} else {
		return nil, false, fmt.Errorf("ping socket: %v; raw socket: %w", err, rawErr)
	}
}

func icmpProtocol(family int) int {
	if family == 6 {
		return icmpProtocolIPv6
	}
	return icmpProtocolIPv4
}

func echoReplyType(family int) icmp.Type {
	if family == 6 {
		return ipv6.ICMPTypeEchoReply
	}
	return ipv4.ICMPTypeEchoReply
}

func sendEcho(conn *icmp.PacketConn, ip net.IP, family, id, seq int, unprivileged bool) {
	var typ icmp.Type = ipv4.ICMPTypeEcho
	if family == 6 {
		typ = ipv6.ICMPTypeEchoRequest
	}
	msg := icmp.Message{
		Type: typ,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  seq,
			Data: []byte("netbox-glean"),
		},
	}
	data, err := msg.Marshal(nil)
	if err != nil {
		return
	}
	var dst net.Addr = &net.IPAddr{IP: ip}
	if unprivileged {
		// ping sockets accept ICMP payloads through a UDP socket, so the
		// destination must be a UDP address.
		dst = &net.UDPAddr{IP: ip}
	}
	_, _ = conn.WriteTo(data, dst)
}

func peerIP(addr net.Addr) net.IP {
	switch value := addr.(type) {
	case *net.IPAddr:
		return value.IP
	case *net.UDPAddr:
		return value.IP
	default:
		return nil
	}
}
