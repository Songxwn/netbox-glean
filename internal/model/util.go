package model

import (
	"fmt"
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
	if t.ScheduleIntervalMinutes <= 0 {
		t.ScheduleIntervalMinutes = DefaultScheduleMinutes
	}
	if t.ScheduleIntervalMinutes < MinScheduleMinutes {
		t.ScheduleIntervalMinutes = MinScheduleMinutes
	}
	if t.ScheduleIntervalMinutes > MaxScheduleMinutes {
		t.ScheduleIntervalMinutes = MaxScheduleMinutes
	}
	if t.Name == "" {
		t.Name = t.Address
	}
	t.SNMP.Version = strings.ToLower(strings.TrimSpace(t.SNMP.Version))
	if t.SNMP.Version == "" {
		t.SNMP.Version = "v2c"
	}
	t.SNMP.V3AuthProtocol = strings.ToUpper(strings.TrimSpace(t.SNMP.V3AuthProtocol))
	t.SNMP.V3PrivProtocol = strings.ToUpper(strings.TrimSpace(t.SNMP.V3PrivProtocol))
	t.IPMI.Interface = strings.ToLower(strings.TrimSpace(t.IPMI.Interface))
	if t.IPMI.Interface == "" {
		t.IPMI.Interface = "lanplus"
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