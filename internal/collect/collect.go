// Package collect implements the SNMP, IPMI and Redfish collectors.
package collect

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// Run dispatches a collection for a single target and normalizes the result.
func Run(ctx context.Context, target model.Target, logf model.LogFunc) (*model.Collected, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var (
		collected *model.Collected
		err       error
	)
	switch target.Protocol {
	case model.ProtocolSNMP:
		collected, err = collectSNMP(ctx, target, logf)
	case model.ProtocolIPMI:
		collected, err = collectIPMI(ctx, target, logf)
	case model.ProtocolRedfish:
		collected, err = collectRedfish(ctx, target, logf)
	default:
		return nil, fmt.Errorf("unsupported protocol %q", target.Protocol)
	}
	if err != nil {
		return nil, err
	}
	if collected == nil {
		return nil, fmt.Errorf("采集器未返回数据")
	}
	finish(target, collected)
	return collected, nil
}

func finish(target model.Target, c *model.Collected) {
	if c == nil {
		return
	}
	if c.Source == "" {
		c.Source = target.Protocol
	}
	if c.CollectedAt.IsZero() {
		c.CollectedAt = time.Now().UTC()
	}
	if c.Identity.Name == "" {
		c.Identity.Name = model.First(target.DeviceName, target.Name, target.Address)
	}
	c.Identity.Name = model.Truncate(c.Identity.Name, 64)
	c.Identity.Manufacturer = strings.TrimSpace(c.Identity.Manufacturer)
	c.Identity.Model = strings.TrimSpace(c.Identity.Model)
	c.Identity.Serial = strings.TrimSpace(c.Identity.Serial)
	c.Identity.AssetTag = strings.TrimSpace(c.Identity.AssetTag)
	c.Identity.UUID = strings.TrimSpace(c.Identity.UUID)
	if c.Identity.MemoryBytes > 0 && c.Identity.MemoryGB == 0 {
		c.Identity.MemoryGB = math.Round(float64(c.Identity.MemoryBytes)/(1024*1024*1024)*100) / 100
	}

	seen := map[string]int{}
	ifaces := make([]model.Interface, 0, len(c.Interfaces))
	for _, iface := range c.Interfaces {
		iface.Name = strings.TrimSpace(iface.Name)
		if iface.Name == "" {
			continue
		}
		key := strings.ToLower(iface.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = 1
		ifaces = append(ifaces, iface)
	}
	c.Interfaces = ifaces

	components := make([]model.Component, 0, len(c.Components))
	compSeen := map[string]int{}
	for _, comp := range c.Components {
		comp.Name = strings.TrimSpace(comp.Name)
		if comp.Name == "" {
			continue
		}
		key := strings.ToLower(comp.Class) + "|" + strings.ToLower(comp.Name)
		if n := compSeen[key]; n > 0 {
			compSeen[key] = n + 1
			comp.Name = model.Truncate(comp.Name, 56) + " #" + fmt.Sprint(n+1)
			key = strings.ToLower(comp.Class) + "|" + strings.ToLower(comp.Name)
		} else {
			compSeen[key] = 1
		}
		components = append(components, comp)
		if len(components) >= 2000 {
			c.Warnings = append(c.Warnings, "组件数量超过 2000，后续组件已忽略")
			break
		}
	}
	c.Components = components
}