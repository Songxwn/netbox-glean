package collect

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/Songxwn/netbox-glean/internal/model"
)

const (
	oidSysDescr    = ".1.3.6.1.2.1.1.1.0"
	oidSysObjectID = ".1.3.6.1.2.1.1.2.0"
	oidSysUpTime   = ".1.3.6.1.2.1.1.3.0"
	oidSysContact  = ".1.3.6.1.2.1.1.4.0"
	oidSysName     = ".1.3.6.1.2.1.1.5.0"
	oidSysLocation = ".1.3.6.1.2.1.1.6.0"

	oidEntDescr      = ".1.3.6.1.2.1.47.1.1.1.1.2"
	oidEntContained  = ".1.3.6.1.2.1.47.1.1.1.1.4"
	oidEntClass      = ".1.3.6.1.2.1.47.1.1.1.1.5"
	oidEntName       = ".1.3.6.1.2.1.47.1.1.1.1.7"
	oidEntHardware   = ".1.3.6.1.2.1.47.1.1.1.1.8"
	oidEntFirmware   = ".1.3.6.1.2.1.47.1.1.1.1.9"
	oidEntSoftware   = ".1.3.6.1.2.1.47.1.1.1.1.10"
	oidEntSerial     = ".1.3.6.1.2.1.47.1.1.1.1.11"
	oidEntMfg        = ".1.3.6.1.2.1.47.1.1.1.1.12"
	oidEntModel      = ".1.3.6.1.2.1.47.1.1.1.1.13"
	oidEntAssetTag   = ".1.3.6.1.2.1.47.1.1.1.1.15"

	oidIfDescr       = ".1.3.6.1.2.1.2.2.1.2"
	oidIfType        = ".1.3.6.1.2.1.2.2.1.3"
	oidIfMTU         = ".1.3.6.1.2.1.2.2.1.4"
	oidIfSpeed       = ".1.3.6.1.2.1.2.2.1.5"
	oidIfPhysAddress = ".1.3.6.1.2.1.2.2.1.6"
	oidIfAdminStatus = ".1.3.6.1.2.1.2.2.1.7"
	oidIfOperStatus  = ".1.3.6.1.2.1.2.2.1.8"
	oidIfHighSpeed   = ".1.3.6.1.2.1.31.1.1.1.15"
	oidIfAlias       = ".1.3.6.1.2.1.31.1.1.1.18"
	oidIfName        = ".1.3.6.1.2.1.31.1.1.1.1"
)

type oidColumn struct {
	field string
	oid   string
}

var entityColumns = []oidColumn{
	{"descr", oidEntDescr},
	{"containedin", oidEntContained},
	{"class", oidEntClass},
	{"name", oidEntName},
	{"hardware", oidEntHardware},
	{"firmware", oidEntFirmware},
	{"software", oidEntSoftware},
	{"serial", oidEntSerial},
	{"mfg", oidEntMfg},
	{"model", oidEntModel},
	{"asset", oidEntAssetTag},
}

var interfaceColumns = []oidColumn{
	{"name", oidIfName},
	{"descr", oidIfDescr},
	{"type", oidIfType},
	{"mtu", oidIfMTU},
	{"speed", oidIfSpeed},
	{"highspeed", oidIfHighSpeed},
	{"mac", oidIfPhysAddress},
	{"admin", oidIfAdminStatus},
	{"oper", oidIfOperStatus},
	{"alias", oidIfAlias},
}

func collectSNMP(ctx context.Context, target model.Target, logf model.LogFunc) (*model.Collected, error) {
	port := target.Port
	if port == 0 {
		port = 161
	}
	params, err := buildSNMPParams(target)
	if err != nil {
		return nil, err
	}
	if err := params.Connect(); err != nil {
		return nil, fmt.Errorf("SNMP 连接 %s:%d 失败: %w", target.Address, port, err)
	}
	defer func() {
		if params.Conn != nil {
			_ = params.Conn.Close()
		}
	}()
	logf("SNMP 已连接 %s:%d (%s)", target.Address, port, target.SNMP.Version)

	out := &model.Collected{
		Source:      model.ProtocolSNMP,
		CollectedAt: time.Now().UTC(),
		Extra:       map[string]string{},
	}
	sys := map[string]string{}
	sysOIDs := map[string]string{
		"descr":    oidSysDescr,
		"objectid": oidSysObjectID,
		"uptime":   oidSysUpTime,
		"contact":  oidSysContact,
		"name":     oidSysName,
		"location": oidSysLocation,
	}
	var firstErr error
	for key, oid := range sysOIDs {
		value, err := snmpGetOne(params, oid)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			logf("读取 %s 失败: %v", oid, err)
			continue
		}
		sys[key] = value
	}

	bulk := params.Version != gosnmp.Version1
	entity := walkTable(ctx, params, entityColumns, bulk, out, logf)
	ifaceTable := walkTable(ctx, params, interfaceColumns, bulk, out, logf)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	id := &out.Identity
	id.Name = sys["name"]
	id.OS = sys["descr"]
	if root := pickIdentityEntity(entity); root != nil {
		id.Manufacturer = model.First(root["mfg"])
		id.Model = model.First(root["model"])
		id.Serial = model.First(root["serial"])
		id.AssetTag = model.First(root["asset"])
		if id.OS == "" {
			id.OS = root["descr"]
		}
	}
	if id.Manufacturer == "" || id.Model == "" || id.Serial == "" {
		for _, idx := range sortedIndexes(entity) {
			e := entity[idx]
			if id.Manufacturer == "" {
				id.Manufacturer = model.First(e["mfg"])
			}
			if id.Model == "" {
				id.Model = model.First(e["model"])
			}
			if id.Serial == "" {
				id.Serial = model.First(e["serial"])
			}
			if id.Manufacturer != "" && id.Model != "" && id.Serial != "" {
				break
			}
		}
	}
	if id.AssetTag == "" {
		for _, idx := range sortedIndexes(entity) {
			if v := model.First(entity[idx]["asset"]); v != "" {
				id.AssetTag = v
				break
			}
		}
	}
	if id.Firmware == "" {
		for _, idx := range sortedIndexes(entity) {
			e := entity[idx]
			if v := model.First(e["software"], e["firmware"]); v != "" {
				id.Firmware = v
				break
			}
		}
	}
	if id.CPUCores == 0 {
		for _, e := range entity {
			if e["class"] == "12" {
				id.CPUCores++
				if id.CPUModel == "" {
					id.CPUModel = model.First(e["model"], e["descr"])
				}
			}
		}
	}
	for key, value := range sys {
		if value != "" {
			out.Extra["sys_"+key] = value
		}
	}
	if sys["location"] != "" {
		out.Extra["location"] = sys["location"]
	}
	if sys["contact"] != "" {
		out.Extra["contact"] = sys["contact"]
	}

	if len(entity) > 0 {
		for _, idx := range sortedIndexes(entity) {
			e := entity[idx]
			class := snmpEntityClass(e["class"])
			if class == "other" || class == "port" || class == "container" {
				continue
			}
			name := model.First(e["name"], e["descr"])
			if name == "" {
				if model.First(e["model"], e["serial"], e["descr"]) == "" {
					continue
				}
				name = class + " " + idx
			}
			comp := model.Component{
				Name:         name,
				Class:        class,
				Manufacturer: e["mfg"],
				Model:        e["model"],
				Serial:       model.First(e["serial"]),
				AssetTag:     e["asset"],
				PartNumber:   model.First(e["model"]),
				Description:  model.First(e["descr"], class),
				Firmware:     model.First(e["software"], e["firmware"]),
			}
			if parentIdx := e["containedin"]; parentIdx != "" && parentIdx != "0" {
				if parent, ok := entity[parentIdx]; ok {
					comp.Parent = model.First(parent["name"], parent["descr"])
				}
			}
			out.Components = append(out.Components, comp)
		}
	}

	for _, idx := range sortedIndexes(ifaceTable) {
		e := ifaceTable[idx]
		name := model.First(e["name"], e["descr"])
		if name == "" {
			continue
		}
		ifType, _ := strconv.Atoi(e["type"])
		speedMbps := 0
		if n, err := strconv.Atoi(e["highspeed"]); err == nil && n > 0 {
			speedMbps = n
		} else if n, err := strconv.Atoi(e["speed"]); err == nil && n > 0 {
			speedMbps = n / 1_000_000
		}
		mtu, _ := strconv.Atoi(e["mtu"])
		admin := e["admin"] == "" || e["admin"] == "1"
		oper := e["oper"] == "" || e["oper"] == "1"
		out.Interfaces = append(out.Interfaces, model.Interface{
			Name:        name,
			Description: model.First(e["alias"], e["descr"]),
			Alias:       e["alias"],
			Type:        netboxInterfaceType(ifType, speedMbps),
			MAC:         snmpMAC(e["mac"]),
			MTU:         mtu,
			SpeedMbps:   speedMbps,
			AdminUp:     admin,
			OperUp:      oper,
		})
	}
	sort.Slice(out.Interfaces, func(i, j int) bool {
		return out.Interfaces[i].Name < out.Interfaces[j].Name
	})

	collectLLDP(params, ifaceTable, bulk, out, logf)

	if len(sys) == 0 && len(entity) == 0 && len(out.Interfaces) == 0 {
		if firstErr != nil {
			return nil, fmt.Errorf("SNMP 未返回可用数据: %w", firstErr)
		}
		return nil, fmt.Errorf("SNMP 未返回可用数据")
	}
	logf("SNMP 采集完成: 系统字段 %d, 组件 %d, 接口 %d, LLDP 邻居 %d", len(sys), len(out.Components), len(out.Interfaces), len(out.Neighbors))
	return out, nil
}

func buildSNMPParams(target model.Target) (*gosnmp.GoSNMP, error) {
	return buildSNMPParamsFor(target.Address, target.Port, target.SNMP)
}

func buildSNMPParamsFor(address string, port int, cfg model.SNMPConfig) (*gosnmp.GoSNMP, error) {
	if port == 0 {
		port = 161
	}
	params := &gosnmp.GoSNMP{
		Target:         address,
		Port:           uint16(port),
		Timeout:        5 * time.Second,
		Retries:        2,
		MaxRepetitions: 25,
		MaxOids:        gosnmp.MaxOids,
	}
	switch strings.ToLower(cfg.Version) {
	case "", "2", "2c", "v2", "v2c":
		params.Version = gosnmp.Version2c
		params.Community = cfg.Community
	case "1", "v1":
		params.Version = gosnmp.Version1
		params.Community = cfg.Community
	case "3", "v3":
		params.Version = gosnmp.Version3
		params.SecurityModel = gosnmp.UserSecurityModel
		params.MsgFlags = gosnmp.AuthPriv
		if cfg.V3AuthPassword == "" {
			params.MsgFlags = gosnmp.NoAuthNoPriv
		} else if cfg.V3PrivPassword == "" {
			params.MsgFlags = gosnmp.AuthNoPriv
		}
		params.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 cfg.V3Username,
			AuthenticationProtocol:   snmpAuthProtocol(cfg.V3AuthProtocol),
			AuthenticationPassphrase: cfg.V3AuthPassword,
			PrivacyProtocol:          snmpPrivProtocol(cfg.V3PrivProtocol),
			PrivacyPassphrase:        cfg.V3PrivPassword,
		}
	default:
		return nil, fmt.Errorf("不支持的 SNMP 版本 %q", cfg.Version)
	}
	return params, nil
}

func snmpAuthProtocol(name string) gosnmp.SnmpV3AuthProtocol {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "MD5":
		return gosnmp.MD5
	case "SHA", "SHA1":
		return gosnmp.SHA
	case "SHA224":
		return gosnmp.SHA224
	case "SHA384":
		return gosnmp.SHA384
	case "SHA512":
		return gosnmp.SHA512
	default:
		return gosnmp.SHA256
	}
}

func snmpPrivProtocol(name string) gosnmp.SnmpV3PrivProtocol {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "DES":
		return gosnmp.DES
	case "AES192":
		return gosnmp.AES192
	case "AES256":
		return gosnmp.AES256
	case "AES192C":
		return gosnmp.AES192C
	case "AES256C":
		return gosnmp.AES256C
	default:
		return gosnmp.AES
	}
}

func snmpGetOne(params *gosnmp.GoSNMP, oid string) (string, error) {
	packet, err := params.Get([]string{oid})
	if err != nil {
		return "", err
	}
	if packet == nil || len(packet.Variables) == 0 {
		return "", nil
	}
	return snmpString(packet.Variables[0].Value), nil
}

func walkTable(ctx context.Context, params *gosnmp.GoSNMP, columns []oidColumn, bulk bool, out *model.Collected, logf model.LogFunc) map[string]map[string]string {
	table := map[string]map[string]string{}
	failures := 0
	var firstErr error
	for _, col := range columns {
		if err := ctx.Err(); err != nil {
			return table
		}
		values, err := walkColumn(params, col.oid, bulk)
		if err != nil {
			failures++
			if firstErr == nil {
				firstErr = err
			}
			logf("SNMP walk %s 失败: %v", col.oid, err)
			continue
		}
		for index, value := range values {
			row := table[index]
			if row == nil {
				row = map[string]string{}
				table[index] = row
			}
			row[col.field] = value
		}
	}
	if failures == len(columns) && firstErr != nil {
		out.Warnings = append(out.Warnings, "SNMP 表读取失败: "+firstErr.Error())
	}
	return table
}

func walkColumn(params *gosnmp.GoSNMP, oid string, bulk bool) (map[string]string, error) {
	values := map[string]string{}
	handler := func(pdu gosnmp.SnmpPDU) error {
		index := oidSuffix(oid, pdu.Name)
		if index == "" {
			return nil
		}
		values[index] = snmpString(pdu.Value)
		return nil
	}
	var err error
	if bulk {
		err = params.BulkWalk(oid, handler)
		if err != nil && len(values) == 0 {
			err = params.Walk(oid, handler)
		}
	} else {
		err = params.Walk(oid, handler)
	}
	return values, err
}

func oidSuffix(prefix, oid string) string {
	prefix = strings.Trim(prefix, ".")
	oid = strings.Trim(oid, ".")
	if strings.HasPrefix(oid, prefix+".") {
		return strings.TrimPrefix(oid, prefix+".")
	}
	return ""
}

func snmpString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimRight(v, "\x00")
	case []byte:
		return strings.TrimRight(string(v), "\x00")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func snmpMAC(value string) string {
	if value == "" {
		return ""
	}
	if mac, err := net.ParseMAC(strings.TrimSpace(value)); err == nil {
		return strings.ToLower(mac.String())
	}
	if len(value) == 6 {
		return strings.ToLower(net.HardwareAddr([]byte(value)).String())
	}
	return ""
}

func sortedIndexes(table map[string]map[string]string) []string {
	indexes := make([]string, 0, len(table))
	for idx := range table {
		indexes = append(indexes, idx)
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		a, errA := strconv.Atoi(indexes[i])
		b, errB := strconv.Atoi(indexes[j])
		if errA == nil && errB == nil {
			return a < b
		}
		return indexes[i] < indexes[j]
	})
	return indexes
}

func pickIdentityEntity(table map[string]map[string]string) map[string]string {
	var fallback map[string]string
	for _, idx := range sortedIndexes(table) {
		e := table[idx]
		if e["class"] == "3" && (e["serial"] != "" || e["model"] != "") {
			return e
		}
		if fallback == nil && (e["serial"] != "" || e["model"] != "") {
			fallback = e
		}
	}
	if fallback != nil {
		return fallback
	}
	if root, ok := table["1"]; ok {
		return root
	}
	return nil
}

func snmpEntityClass(value string) string {
	switch value {
	case "3":
		return "chassis"
	case "4":
		return "backplane"
	case "5":
		return "container"
	case "6":
		return "power-supply"
	case "7":
		return "fan"
	case "8":
		return "sensor"
	case "9":
		return "module"
	case "10":
		return "port"
	case "11":
		return "stack"
	case "12":
		return "cpu"
	case "13":
		return "energy-storage"
	default:
		return "other"
	}
}

func netboxInterfaceType(ifType, speedMbps int) string {
	switch ifType {
	case 161:
		return "lag"
	case 24, 53:
		return "virtual"
	case 131:
		return "tunnel"
	case 6, 0:
	default:
		return "other"
	}
	switch {
	case speedMbps >= 100_000:
		return "100gbase-x-qsfp28"
	case speedMbps >= 40_000:
		return "40gbase-x-qsfpp"
	case speedMbps >= 25_000:
		return "25gbase-x-sfp28"
	case speedMbps >= 10_000:
		return "10gbase-x-sfpp"
	case speedMbps >= 1_000:
		return "1000base-t"
	case speedMbps >= 100:
		return "100base-tx"
	case speedMbps >= 10:
		return "10base-t"
	default:
		return "other"
	}
}