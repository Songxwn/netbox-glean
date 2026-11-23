package collect

import (
	"bytes"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
	"github.com/Songxwn/netbox-glean/internal/model"
)

// IP-MIB address translation tables. ipNetToMediaTable is the legacy IPv4
// ARP table; ipNetToPhysicalTable also covers IPv6 neighbor discovery.
const (
	ipNetToMediaPrefix = ".1.3.6.1.2.1.4.22.1"

	oidARPIfIndex     = ipNetToMediaPrefix + ".1"
	oidARPPhysAddress = ipNetToMediaPrefix + ".2"
	oidARPNetAddress  = ipNetToMediaPrefix + ".3"
	oidARPType        = ipNetToMediaPrefix + ".4"

	ipNetToPhysicalPrefix = ".1.3.6.1.2.1.4.35.1"

	oidNDPIfIndex     = ipNetToPhysicalPrefix + ".1"
	oidNDPPhysAddress = ipNetToPhysicalPrefix + ".4"
	oidNDPType        = ipNetToPhysicalPrefix + ".6"
	oidNDPState       = ipNetToPhysicalPrefix + ".7"

	// arpMaxEntries bounds the amount of neighbor data kept per device.
	arpMaxEntries = 4096
)

var arpColumns = []oidColumn{
	{"ifindex", oidARPIfIndex},
	{"mac", oidARPPhysAddress},
	{"net", oidARPNetAddress},
	{"type", oidARPType},
}

var ndpColumns = []oidColumn{
	{"ifindex", oidNDPIfIndex},
	{"mac", oidNDPPhysAddress},
	{"type", oidNDPType},
	{"state", oidNDPState},
}

// collectNeighborTables reads the ARP and NDP tables of a device. Every
// failure is logged and skipped so the caller can still use the other table.
func collectNeighborTables(params *gosnmp.GoSNMP, ifaces map[string]map[string]string, bulk bool, logf model.LogFunc) []model.IPAddress {
	arp := collectNeighborTable(params, arpColumns, bulk, "ARP", ifaces, logf)
	ndp := collectNeighborTable(params, ndpColumns, bulk, "NDP", ifaces, logf)
	out := make([]model.IPAddress, 0, len(arp)+len(ndp))
	out = append(out, arp...)
	out = append(out, ndp...)
	if len(out) > 0 {
		logf("ARP/邻居表: ARP %d 条, NDP %d 条", len(arp), len(ndp))
	}
	return out
}

func collectNeighborTable(params *gosnmp.GoSNMP, columns []oidColumn, bulk bool, name string, ifaces map[string]map[string]string, logf model.LogFunc) []model.IPAddress {
	table := map[string]map[string]string{}
	ok := 0
	var firstErr error
	for _, col := range columns {
		values, err := walkColumn(params, col.oid, bulk)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if len(values) == 0 {
			continue
		}
		ok++
		for index, value := range values {
			row := table[index]
			if row == nil {
				row = map[string]string{}
				table[index] = row
			}
			row[col.field] = value
		}
	}
	if ok == 0 {
		if firstErr != nil {
			logf("读取 %s 表失败: %v", name, firstErr)
		}
		return nil
	}
	entries := buildIPAddresses(table, ifaces, name == "NDP")
	if len(entries) > 0 {
		return entries
	}
	return nil
}

// buildIPAddresses converts the raw address translation table into normalized
// entries. It is separated from SNMP I/O so it can be unit tested.
func buildIPAddresses(table, ifaces map[string]map[string]string, ndp bool) []model.IPAddress {
	if len(table) == 0 {
		return nil
	}
	indexes := make([]string, 0, len(table))
	for index := range table {
		indexes = append(indexes, index)
	}
	sort.Strings(indexes)

	out := make([]model.IPAddress, 0, len(indexes))
	seen := map[string]bool{}
	for _, index := range indexes {
		row := table[index]
		if strings.TrimSpace(row["type"]) == "2" {
			continue
		}
		if ndp {
			// invalid(5) and incomplete(7) neighbor entries carry no
			// usable address mapping.
			switch strings.TrimSpace(row["state"]) {
			case "5", "7":
				continue
			}
		}
		ifIndex, indexIP, indexOK := parseNeighborIndex(index)
		// Prefer the address column; it is authoritative when the device
		// encodes the index in an unexpected way.
		ip := snmpIPAddress(row["net"])
		if ip == nil {
			if !indexOK {
				continue
			}
			ip = indexIP
		} else if ifIndex == 0 {
			ifIndex, _ = strconv.Atoi(strings.SplitN(index, ".", 2)[0])
		}
		if !usableIP(ip) {
			continue
		}
		address := ip.String()
		key := strings.ToLower(address)
		if seen[key] {
			continue
		}
		seen[key] = true

		ifaceName := ""
		if iface, ok := ifaces[strconv.Itoa(ifIndex)]; ok {
			ifaceName = model.First(iface["name"], iface["descr"])
		}
		source := "arp"
		if ndp {
			source = "ndp"
		}
		out = append(out, model.IPAddress{
			Address:   address,
			MAC:       neighborMAC(row["mac"]),
			Interface: ifaceName,
			IfIndex:   ifIndex,
			Source:    source,
			State:     strings.TrimSpace(row["state"]),
		})
		if len(out) >= arpMaxEntries {
			break
		}
	}
	return out
}

// parseNeighborIndex extracts ifIndex and the IP from an address translation
// table index. ipNetToMediaTable rows are "ifIndex.a.b.c.d"; agents that also
// return ipNetToPhysicalTable entries typically use the RFC 4293 index
// "ifIndex.addressType.length.a.b.c.d". The length prefix is tolerated even
// without an address type because some agents emit only one of them.
func parseNeighborIndex(index string) (int, net.IP, bool) {
	parts := strings.Split(strings.Trim(index, "."), ".")
	if len(parts) < 2 {
		return 0, nil, false
	}
	ifIndex, err := strconv.Atoi(parts[0])
	if err != nil || ifIndex <= 0 {
		return 0, nil, false
	}
	rest := parts[1:]

	// Try the plain address first so an IPv4 address such as 1.2.3.4 is not
	// mistaken for an address type or length prefix.
	if ip, ok := ipFromOctets(rest); ok {
		return ifIndex, ip, true
	}
	for _, skip := range []int{1, 2} {
		if len(rest) <= skip {
			continue
		}
		if skip == 1 {
			// ifIndex.length.address
			if !isLengthPrefix(rest[0]) {
				continue
			}
		} else {
			// ifIndex.addressType.length.address
			if !isAddressType(rest[0]) || !isLengthPrefix(rest[1]) {
				continue
			}
		}
		if ip, ok := ipFromOctets(rest[skip:]); ok {
			return ifIndex, ip, true
		}
	}
	return 0, nil, false
}

func ipFromOctets(octets []string) (net.IP, bool) {
	if len(octets) != 4 && len(octets) != 16 {
		return nil, false
	}
	raw := make([]byte, len(octets))
	for i, part := range octets {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return nil, false
		}
		raw[i] = byte(value)
	}
	if len(raw) == 4 {
		return net.IPv4(raw[0], raw[1], raw[2], raw[3]), true
	}
	return net.IP(raw), true
}

func isAddressType(value string) bool {
	switch value {
	case "1", "2", "3", "4", "16":
		return true
	default:
		return false
	}
}

func isLengthPrefix(value string) bool {
	return value == "4" || value == "16"
}

// neighborMAC normalizes the physAddress column. RFC 4293 allows agents to
// prefix the hardware address with its length; strip that byte when present.
func neighborMAC(value string) string {
	if value == "" {
		return ""
	}
	raw := []byte(value)
	if len(raw) == 7 && raw[0] == 6 {
		raw = raw[1:]
	}
	if len(raw) == 6 {
		return strings.ToLower(net.HardwareAddr(raw).String())
	}
	if mac, err := net.ParseMAC(strings.TrimSpace(value)); err == nil {
		return strings.ToLower(mac.String())
	}
	return ""
}

// snmpIPAddress converts either a textual IP address or raw octets into a
// net.IP. gosnmp returns IpAddress values as strings and InetAddress values
// as byte slices, so both forms are accepted.
func snmpIPAddress(value string) net.IP {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip
	}
	raw := []byte(value)
	switch len(raw) {
	case 4, 16:
		return net.IP(raw)
	default:
		return nil
	}
}

// usableIP rejects addresses that are not useful for IPAM discovery.
func usableIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return false
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4.Equal(net.IPv4bcast) {
		return false
	}
	return true
}

// normalizeIPAddresses merges duplicate discoveries and keeps the entry with
// the richest metadata (an ARP row wins over a bare ICMP reply).
func normalizeIPAddresses(in []model.IPAddress) []model.IPAddress {
	if len(in) == 0 {
		return nil
	}
	byAddress := map[string]model.IPAddress{}
	order := make([]string, 0, len(in))
	for _, entry := range in {
		ip := net.ParseIP(strings.TrimSpace(entry.Address))
		if !usableIP(ip) {
			continue
		}
		entry.Address = ip.String()
		entry.Interface = strings.TrimSpace(entry.Interface)
		entry.MAC = neighborMAC(entry.MAC)
		key := strings.ToLower(entry.Address)
		if existing, ok := byAddress[key]; ok {
			if existing.MAC == "" {
				existing.MAC = entry.MAC
			}
			if existing.Interface == "" {
				existing.Interface = entry.Interface
			}
			if existing.IfIndex == 0 {
				existing.IfIndex = entry.IfIndex
			}
			if existing.State == "" {
				existing.State = entry.State
			}
			byAddress[key] = existing
			continue
		}
		byAddress[key] = entry
		order = append(order, key)
	}

	out := make([]model.IPAddress, 0, len(order))
	for _, key := range order {
		out = append(out, byAddress[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		a := net.ParseIP(out[i].Address)
		b := net.ParseIP(out[j].Address)
		if a == nil || b == nil {
			return out[i].Address < out[j].Address
		}
		return bytes.Compare(a.To16(), b.To16()) < 0
	})
	if len(out) > arpMaxEntries {
		out = out[:arpMaxEntries]
	}
	return out
}