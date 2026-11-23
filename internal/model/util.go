package model

import (
	"fmt"
	"net"
	"strings"
	"unicode/utf8"
)

// Sprintf is a tiny indirection so model does not need to import fmt in
// several files while keeping the Add/Warn helpers readable.
func Sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// First returns the first non-empty trimmed string.
func First(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// Truncate limits a string to at most n runes.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// DefaultPort returns the default TCP/UDP port for a protocol.
func DefaultPort(protocol string) int {
	switch strings.ToLower(protocol) {
	case ProtocolSNMP:
		return 161
	case ProtocolIPMI:
		return 623
	case ProtocolRedfish:
		return 443
	default:
		return 0
	}
}

// MaxScanHosts bounds one batch device scan.
const MaxScanHosts = 256

// ScanCIDRs returns the normalized ICMP sweep subnets of an IP target.
func (t IPTarget) ScanCIDRs() []string {
	return SplitList(t.Subnets)
}

// ScanCIDRs returns the normalized subnets of a batch scan.
func (r ScanRequest) ScanCIDRs() []string {
	return SplitList(r.Subnets)
}

// NormalizeSubnets collapses the supported separators into a comma separated
// list and removes duplicates.
func NormalizeSubnets(value string) string {
	return strings.Join(SplitList(value), ", ")
}

// SplitList splits a comma, semicolon or whitespace separated list.
func SplitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ';', ' ', '\t', '\r', '\n':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

func normalizeSNMPConfig(c *SNMPConfig) {
	c.Version = strings.ToLower(strings.TrimSpace(c.Version))
	if c.Version == "" {
		c.Version = "v2c"
	}
	c.Community = strings.TrimSpace(c.Community)
	c.V3Username = strings.TrimSpace(c.V3Username)
	c.V3AuthProtocol = strings.ToUpper(strings.TrimSpace(c.V3AuthProtocol))
	c.V3PrivProtocol = strings.ToUpper(strings.TrimSpace(c.V3PrivProtocol))
}

// NormalizeTarget fills in defaults and strips accidental whitespace.
func NormalizeTarget(t *Target) {
	t.Name = strings.TrimSpace(t.Name)
	t.Address = strings.TrimSpace(t.Address)
	t.Protocol = strings.ToLower(strings.TrimSpace(t.Protocol))
	t.Site = strings.TrimSpace(t.Site)
	t.DeviceRole = strings.TrimSpace(t.DeviceRole)
	t.Platform = strings.TrimSpace(t.Platform)
	t.DeviceName = strings.TrimSpace(t.DeviceName)
	t.Description = strings.TrimSpace(t.Description)
	if t.Port == 0 {
		t.Port = DefaultPort(t.Protocol)
	}
	t.ScheduleIntervalMinutes = clampScheduleMinutes(t.ScheduleIntervalMinutes)
	if t.Name == "" {
		t.Name = t.Address
	}
	normalizeSNMPConfig(&t.SNMP)
	t.IPMI.Interface = strings.ToLower(strings.TrimSpace(t.IPMI.Interface))
	if t.IPMI.Interface == "" {
		t.IPMI.Interface = "lanplus"
	}
}

// NormalizeIPTarget fills in defaults for an independent IP discovery target.
func NormalizeIPTarget(t *IPTarget) {
	t.Name = strings.TrimSpace(t.Name)
	t.Subnets = NormalizeSubnets(t.Subnets)
	t.Address = strings.TrimSpace(t.Address)
	if t.Address != "" && t.Port == 0 {
		t.Port = 161
	}
	t.ScheduleIntervalMinutes = clampScheduleMinutes(t.ScheduleIntervalMinutes)
	normalizeSNMPConfig(&t.SNMP)
	if t.Name == "" {
		t.Name = First(t.Address, t.Subnets, "IP 采集")
	}
}

// NormalizeScanRequest fills in defaults for a batch device scan.
func NormalizeScanRequest(r *ScanRequest) {
	r.Subnets = NormalizeSubnets(r.Subnets)
	r.Protocol = strings.ToLower(strings.TrimSpace(r.Protocol))
	r.Site = strings.TrimSpace(r.Site)
	r.DeviceRole = strings.TrimSpace(r.DeviceRole)
	r.Platform = strings.TrimSpace(r.Platform)
	r.NamePrefix = strings.TrimSpace(r.NamePrefix)
	if r.Port == 0 {
		r.Port = DefaultPort(r.Protocol)
	}
	normalizeSNMPConfig(&r.SNMP)
	r.IPMI.Interface = strings.ToLower(strings.TrimSpace(r.IPMI.Interface))
	if r.IPMI.Interface == "" {
		r.IPMI.Interface = "lanplus"
	}
}

// ValidateTarget checks the minimum requirements for a target.
func ValidateTarget(t Target) error {
	switch t.Protocol {
	case ProtocolSNMP, ProtocolIPMI, ProtocolRedfish:
	default:
		return fmt.Errorf("unsupported protocol %q", t.Protocol)
	}
	if t.Address == "" {
		return fmt.Errorf("address is required")
	}
	if t.Port < 0 || t.Port > 65535 {
		return fmt.Errorf("invalid port %d", t.Port)
	}
	return nil
}

// ValidateIPTarget checks an independent IP discovery target.
func ValidateIPTarget(t IPTarget) error {
	if t.Address == "" && len(t.ScanCIDRs()) == 0 {
		return fmt.Errorf("请至少填写一个扫描网段或 SNMP 来源地址")
	}
	if t.Port < 0 || t.Port > 65535 {
		return fmt.Errorf("invalid port %d", t.Port)
	}
	return validateIPv4CIDRs(t.ScanCIDRs())
}

// ValidateScanRequest checks a batch device scan.
func ValidateScanRequest(r ScanRequest) error {
	switch r.Protocol {
	case ProtocolSNMP, ProtocolIPMI, ProtocolRedfish:
	default:
		return fmt.Errorf("unsupported protocol %q", r.Protocol)
	}
	if r.Port < 0 || r.Port > 65535 {
		return fmt.Errorf("invalid port %d", r.Port)
	}
	cidrs := r.ScanCIDRs()
	if len(cidrs) == 0 {
		return fmt.Errorf("请填写要扫描的 IPv4 网段")
	}
	if err := validateIPv4CIDRs(cidrs); err != nil {
		return err
	}
	if count := ScanHostCount(cidrs); count > MaxScanHosts {
		return fmt.Errorf("单次批量扫描最多 %d 个地址，请缩小网段（当前约 %d 个）", MaxScanHosts, count)
	}
	return nil
}

func validateIPv4CIDRs(cidrs []string) error {
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("无效的扫描网段 %q: %w", cidr, err)
		}
		if _, bits := network.Mask.Size(); bits != 32 {
			return fmt.Errorf("ICMP 扫描当前仅支持 IPv4 网段: %s", cidr)
		}
	}
	return nil
}

// ScanHostCount returns the approximate number of usable hosts in cidrs. It
// stops counting once MaxScanHosts is exceeded.
func ScanHostCount(cidrs []string) int {
	var total int64
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		ones, bits := network.Mask.Size()
		if bits != 32 {
			continue
		}
		if ones >= 31 {
			total += int64(1) << uint(32-ones)
		} else {
			total += (int64(1) << uint(32-ones)) - 2
		}
		if total > MaxScanHosts {
			return MaxScanHosts + 1
		}
	}
	return int(total)
}