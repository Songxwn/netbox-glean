package collect

import (
	"sort"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
	"github.com/Songxwn/netbox-glean/internal/model"
)

// LLDP-MIB remote table columns. The row index is
// lldpRemTimeMark.lldpRemLocalPortNum.lldpRemIndex, where
// lldpRemLocalPortNum is the ifIndex of the local port.
const (
	lldpRemPrefix = ".1.0.8802.1.1.2.1.4.1.1"

	oidLldpRemChassisID = lldpRemPrefix + ".5"
	oidLldpRemPortID    = lldpRemPrefix + ".7"
	oidLldpRemPortDesc  = lldpRemPrefix + ".8"
	oidLldpRemSysName   = lldpRemPrefix + ".9"
	oidLldpRemSysDesc   = lldpRemPrefix + ".10"
)

// lldpMaxNeighbors bounds the amount of neighbor data kept per device.
const lldpMaxNeighbors = 4096

var lldpColumns = []oidColumn{
	{"port", oidLldpRemPortID},
	{"port_desc", oidLldpRemPortDesc},
	{"sysname", oidLldpRemSysName},
	{"sysdesc", oidLldpRemSysDesc},
	{"chassis", oidLldpRemChassisID},
}

// collectLLDP walks the LLDP remote table and appends the discovered
// neighbors to out. Failures are warnings only: many devices do not run LLDP.
func collectLLDP(params *gosnmp.GoSNMP, ifaces map[string]map[string]string, bulk bool, out *model.Collected, logf model.LogFunc) {
	if out == nil {
		return
	}
	table := map[string]map[string]string{}
	var firstErr error
	ok := 0
	for _, col := range lldpColumns {
		values, err := walkColumn(params, col.oid, bulk)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
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
			logf("LLDP 不可用: %v", firstErr)
		}
		return
	}
	neighbors := buildNeighbors(table, ifaces)
	if len(neighbors) > 0 {
		out.Neighbors = append(out.Neighbors, neighbors...)
		logf("LLDP 邻居: %d 个", len(neighbors))
	}
}

// buildNeighbors converts the raw LLDP remote table into normalized
// neighbors. It is separated from SNMP I/O so it can be unit tested.
func buildNeighbors(table, ifaces map[string]map[string]string) []model.Neighbor {
	if len(table) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]model.Neighbor, 0, len(table))
	indexes := make([]string, 0, len(table))
	for index := range table {
		indexes = append(indexes, index)
	}
	sort.Strings(indexes)
	for _, index := range indexes {
		row := table[index]
		remotePort := strings.TrimSpace(row["port"])
		remoteDesc := strings.TrimSpace(row["port_desc"])
		remoteSystem := strings.TrimSpace(row["sysname"])
		if remotePort == "" && remoteDesc == "" && remoteSystem == "" {
			continue
		}
		localPort, ok := lldpLocalPort(index)
		if !ok {
			continue
		}
		localName := ""
		if iface, ok := ifaces[localPort]; ok {
			localName = model.First(iface["name"], iface["descr"])
		}
		if localName == "" {
			// Fall back to the ifIndex so the neighbor is still visible.
			localName = "ifIndex " + localPort
		}
		key := strings.ToLower(localName) + "|" + strings.ToLower(remoteSystem) + "|" + strings.ToLower(model.First(remotePort, remoteDesc))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, model.Neighbor{
			LocalInterface:  localName,
			LocalPortID:     localPort,
			RemoteSystem:    remoteSystem,
			RemotePort:      remotePort,
			RemotePortDesc:  remoteDesc,
			RemoteChassisID: strings.TrimSpace(row["chassis"]),
			RemoteSysDesc:   model.Truncate(strings.TrimSpace(row["sysdesc"]), 200),
		})
		if len(out) >= lldpMaxNeighbors {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LocalInterface != out[j].LocalInterface {
			return out[i].LocalInterface < out[j].LocalInterface
		}
		return out[i].RemoteSystem < out[j].RemoteSystem
	})
	return out
}

// lldpLocalPort extracts lldpRemLocalPortNum (the local ifIndex) from an
// LLDP remote table index such as "0.12.1".
func lldpLocalPort(index string) (string, bool) {
	parts := strings.Split(strings.Trim(index, "."), ".")
	if len(parts) < 2 {
		return "", false
	}
	port := strings.TrimSpace(parts[1])
	if port == "" {
		return "", false
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", false
	}
	return port, true
}