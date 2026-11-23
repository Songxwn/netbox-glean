package collect

import (
	"context"
	"fmt"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// RunIPDiscovery performs one independent IP discovery run. It reads the
// ARP/NDP tables of the optional SNMP source device and/or sweeps the
// configured IPv4/IPv6 subnets with ICMP. Results are merged and normalized.
func RunIPDiscovery(ctx context.Context, target model.IPTarget, logf model.LogFunc) (*model.Collected, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cidrs := target.ScanCIDRs()
	if target.Address == "" && len(cidrs) == 0 {
		return nil, fmt.Errorf("IP 采集目标未配置扫描网段或 SNMP 来源")
	}

	out := &model.Collected{
		Source:      model.ProtocolIPScan,
		CollectedAt: time.Now().UTC(),
		Extra:       map[string]string{},
	}

	if target.Address != "" {
		if err := collectIPSource(ctx, target, out, logf); err != nil {
			if len(cidrs) == 0 {
				return nil, err
			}
			message := "SNMP 来源读取失败，已跳过: " + err.Error()
			out.Warnings = append(out.Warnings, message)
			logf("%s", message)
		}
	}

	if len(cidrs) > 0 {
		addresses, scanned, warnings := Sweep(ctx, cidrs, logf)
		out.Warnings = append(out.Warnings, warnings...)
		for _, address := range addresses {
			out.IPAddresses = append(out.IPAddresses, model.IPAddress{
				Address: address,
				Source:  "icmp",
			})
		}
		logf("ICMP/ICMPv6 扫描 %d 个地址，存活 %d 个", scanned, len(addresses))
	}

	out.IPAddresses = normalizeIPAddresses(out.IPAddresses)
	if len(out.IPAddresses) == 0 {
		out.Warnings = append(out.Warnings, "本次未发现任何 IP 地址")
	}
	logf("IP 采集完成: %d 个地址", len(out.IPAddresses))
	return out, nil
}

// collectIPSource reads the ARP/NDP tables of the configured SNMP source.
func collectIPSource(ctx context.Context, target model.IPTarget, out *model.Collected, logf model.LogFunc) error {
	params, err := buildSNMPParamsFor(target.Address, target.Port, target.SNMP)
	if err != nil {
		return err
	}
	if err := params.Connect(); err != nil {
		return fmt.Errorf("SNMP 连接 %s:%d 失败: %w", target.Address, params.Port, err)
	}
	defer func() {
		if params.Conn != nil {
			_ = params.Conn.Close()
		}
	}()
	logf("SNMP 已连接 %s:%d (%s)", target.Address, params.Port, target.SNMP.Version)
	bulk := params.Version != gosnmp.Version1
	// walkTable materializes the interface rows, but the IP job only needs
	// the raw ifIndex -> ifName mapping for the ARP/NDP entries.
	ifaceTable := walkTable(ctx, params, interfaceColumns, bulk, &model.Collected{}, logf)
	if err := ctx.Err(); err != nil {
		return err
	}
	out.IPAddresses = append(out.IPAddresses, collectNeighborTables(params, ifaceTable, bulk, logf)...)
	out.Extra["source_device"] = target.Address
	return nil
}