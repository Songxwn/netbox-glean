package collect

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"github.com/Songxwn/netbox-glean/internal/model"
)

const (
	// pingMaxHosts bounds one sweep so a wide CIDR cannot turn into an
	// unintentional network scan.
	pingMaxHosts    = 1024
	pingConcurrency = 64
	pingTimeout     = 1500 * time.Millisecond
)

// pingSweep probes every usable IPv4 host in cidrs and returns the addresses
// that answered an ICMP echo request. Unsupported or oversized ranges are
// reported as warnings and skipped. If neither an unprivileged ping socket
// nor a raw ICMP socket can be opened the sweep is skipped.
func pingSweep(ctx context.Context, cidrs []string, logf model.LogFunc) ([]string, []string) {
	hosts, warnings := expandScanCIDRs(cidrs)
	if len(hosts) == 0 {
		return nil, warnings
	}
	conn, unprivileged, err := openPingConn()
	if err != nil {
		warnings = append(warnings, "ICMP 扫描不可用（需要 NET_RAW 权限或允许 ping socket）: "+err.Error())
		return nil, warnings
	}
	defer conn.Close()
	logf("ICMP 扫描 %d 个地址", len(hosts))

	deadline := time.Now().Add(pingTimeout)
	if err := conn.SetReadDeadline(deadline); err != nil {
		warnings = append(warnings, "ICMP 扫描设置超时失败: "+err.Error())
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
			msg, err := icmp.ParseMessage(1, buf[:n])
			if err != nil || msg.Type != ipv4.ICMPTypeEchoReply {
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
					sendEcho(conn, ip, id, int(atomic.AddUint32(&seq, 1)&0xffff), unprivileged)
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
	if len(out) == 0 {
		return nil, warnings
	}
	return out, warnings
}

// expandScanCIDRs lists the hosts to probe. Network and broadcast addresses
// are skipped for prefixes up to /30; /31 and /32 are probed as-is.
func expandScanCIDRs(cidrs []string) ([]net.IP, []string) {
	var out []net.IP
	var warnings []string
	for _, cidr := range cidrs {
		ip, network, err := net.ParseCIDR(cidr)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("忽略无效的扫描网段 %q: %v", cidr, err))
			continue
		}
		if ip.To4() == nil {
			warnings = append(warnings, fmt.Sprintf("ICMP 扫描仅支持 IPv4，已忽略 %s", cidr))
			continue
		}
		ones, bits := network.Mask.Size()
		if bits != 32 {
			warnings = append(warnings, fmt.Sprintf("忽略非 IPv4 扫描网段 %q", cidr))
			continue
		}
		total := uint64(1) << uint(32-ones)
		first := uint64(binary.BigEndian.Uint32(network.IP.To4()))
		var start, end uint64
		if ones >= 31 {
			start, end = 0, total-1
		} else {
			start, end = 1, total-2
		}
		for offset := start; offset <= end; offset++ {
			if len(out) >= pingMaxHosts {
				warnings = append(warnings, fmt.Sprintf("单次扫描最多 %d 个地址，剩余网段已忽略", pingMaxHosts))
				return out, warnings
			}
			var buf [4]byte
			binary.BigEndian.PutUint32(buf[:], uint32(first+offset))
			out = append(out, net.IPv4(buf[0], buf[1], buf[2], buf[3]))
		}
	}
	return out, warnings
}

// openPingConn prefers an unprivileged ping socket (Linux/Darwin) and falls
// back to a raw ICMP socket on platforms where that requires privileges. The
// boolean reports whether the connection is a datagram ping socket, which
// requires UDP style destinations when writing.
func openPingConn() (*icmp.PacketConn, bool, error) {
	if conn, err := icmp.ListenPacket("udp4", "0.0.0.0"); err == nil {
		return conn, true, nil
	} else if raw, rawErr := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); rawErr == nil {
		return raw, false, nil
	} else {
		return nil, false, fmt.Errorf("ping socket: %v; raw socket: %w", err, rawErr)
	}
}

func sendEcho(conn *icmp.PacketConn, ip net.IP, id, seq int, unprivileged bool) {
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
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