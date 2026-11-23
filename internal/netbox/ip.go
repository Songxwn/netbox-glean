package netbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// ipMaxPerSync bounds the number of IPAM objects touched by one sync so an
// oversized ARP table cannot stall the job.
const ipMaxPerSync = 1000

// nbIPAddress is the subset of the NetBox IP address serializer used when
// deciding whether an address already exists.
type nbIPAddress struct {
	ID                 int    `json:"id"`
	Address            string `json:"address"`
	AssignedObjectType string `json:"assigned_object_type"`
	AssignedObjectID   int    `json:"assigned_object_id"`
}

// SyncIPs writes IP addresses discovered by a standalone IP discovery run
// into NetBox IPAM. It does not require a device.
func (c *Client) SyncIPs(ctx context.Context, settings model.Settings, source string, ips []model.IPAddress, logf model.LogFunc) (*model.SyncResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	res := &model.SyncResult{}
	created, assigned, skipped, warnings := c.syncIPAddresses(ctx, source, ips, settings.SyncUnmatchedIPs, logf)
	res.IPCreated = created
	res.IPAssigned = assigned
	res.IPSkipped = skipped
	res.Warnings = append(res.Warnings, warnings...)
	if created+assigned+skipped > 0 {
		res.Add("IP 地址: 新建 %d, 分配 %d, 跳过 %d", created, assigned, skipped)
	}
	return res, nil
}

// SyncIPs is the package-level entry point used by the server for IP-only
// jobs. It respects the global sync_ip_addresses switch.
func SyncIPs(ctx context.Context, settings model.Settings, source string, ips []model.IPAddress, logf model.LogFunc) (*model.SyncResult, error) {
	if !settings.SyncIPAddresses {
		res := &model.SyncResult{}
		res.Warn("IP 地址同步已在设置中禁用")
		return res, nil
	}
	client, err := NewFromSettings(settings)
	if err != nil {
		return nil, err
	}
	return client.SyncIPs(ctx, settings, source, ips, logf)
}

// syncIPAddresses writes IPs discovered through ARP/NDP tables and ICMP
// sweeps into NetBox IPAM. When the discovered MAC matches a NetBox
// interface the address is assigned to it; unmatched addresses are skipped
// unless matchUnmatched is set, in which case they are created unassigned.
func (c *Client) syncIPAddresses(ctx context.Context, deviceName string, ips []model.IPAddress, matchUnmatched bool, logf model.LogFunc) (int, int, int, []string) {
	var warnings []string
	warned := map[string]bool{}
	addWarning := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		if warned[message] || len(warnings) >= 25 {
			return
		}
		warned[message] = true
		warnings = append(warnings, message)
	}

	macCache := map[string]int{}
	seen := map[string]bool{}
	created, assigned, skipped := 0, 0, 0
	handled := 0

	for _, entry := range ips {
		if ctx.Err() != nil {
			break
		}
		ip := net.ParseIP(strings.TrimSpace(entry.Address))
		if ip == nil || !usableNetboxIP(ip) {
			skipped++
			continue
		}
		key := strings.ToLower(ip.String())
		if seen[key] {
			continue
		}
		seen[key] = true
		if handled >= ipMaxPerSync {
			addWarning("单次同步最多写入 %d 个 IP，其余地址已忽略", ipMaxPerSync)
			break
		}
		handled++

		ifaceID := 0
		if mac := normalizeMAC(entry.MAC); mac != "" {
			if cached, ok := macCache[mac]; ok {
				ifaceID = cached
			} else {
				id, err := c.findInterfaceByMAC(ctx, mac)
				if err != nil {
					addWarning("按 MAC %s 查询 NetBox 接口失败: %v", mac, err)
				} else {
					macCache[mac] = id
					ifaceID = id
				}
			}
		}
		if ifaceID == 0 && !matchUnmatched {
			skipped++
			continue
		}

		prefix := 32
		if ip.To4() == nil {
			prefix = 128
		}
		if entry.PrefixLength > 0 && entry.PrefixLength <= prefix {
			prefix = entry.PrefixLength
		}
		cidr := fmt.Sprintf("%s/%d", ip.String(), prefix)

		existing, found, err := c.findIPAddressByHost(ctx, ip.String())
		if err != nil {
			addWarning("查询 IP %s 失败: %v", ip, err)
			continue
		}
		if found {
			// Assign a floating address when we can now identify its
			// interface, but never overwrite an existing assignment.
			if ifaceID > 0 && existing.AssignedObjectType == "" && existing.AssignedObjectID == 0 {
				payload := map[string]any{
					"assigned_object_type": "dcim.interface",
					"assigned_object_id":   ifaceID,
				}
				if err := c.do(ctx, http.MethodPatch, "/api/ipam/ip-addresses/"+strconv.Itoa(existing.ID)+"/", payload, nil); err != nil {
					addWarning("分配 IP %s 到接口 #%d 失败: %v", ip, ifaceID, err)
					skipped++
					continue
				}
				assigned++
				logf("已分配 IP %s 到 NetBox 接口 #%d", ip, ifaceID)
				continue
			}
			skipped++
			continue
		}

		payload := map[string]any{
			"address":     cidr,
			"status":      "active",
			"description": ipDescription(deviceName, entry),
		}
		if ifaceID > 0 {
			payload["assigned_object_type"] = "dcim.interface"
			payload["assigned_object_id"] = ifaceID
		}
		var result struct {
			ID int `json:"id"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/ipam/ip-addresses/", payload, &result); err != nil {
			if isDuplicateIPError(err) {
				skipped++
				continue
			}
			addWarning("创建 IP %s 失败: %v", cidr, err)
			continue
		}
		created++
		if ifaceID > 0 {
			logf("已创建 IP %s 并分配给接口 #%d", cidr, ifaceID)
		} else {
			logf("已创建 IP %s（未分配接口）", cidr)
		}
	}
	return created, assigned, skipped, warnings
}

func ipDescription(deviceName string, entry model.IPAddress) string {
	parts := []string{"netbox-glean"}
	if deviceName != "" {
		parts = append(parts, "via "+deviceName)
	}
	if entry.Interface != "" {
		parts = append(parts, entry.Interface)
	}
	if entry.MAC != "" {
		parts = append(parts, entry.MAC)
	}
	if entry.Source != "" {
		parts = append(parts, entry.Source)
	}
	return model.Truncate(strings.Join(parts, " "), 200)
}

// findIPAddressByHost searches by host address so a prefix mismatch (an
// existing /24 object found while syncing a /32 host) is still detected.
func (c *Client) findIPAddressByHost(ctx context.Context, host string) (nbIPAddress, bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	raws, err := c.listAll(ctx, "/api/ipam/ip-addresses/", url.Values{
		"q":     {host},
		"limit": {"10"},
	}, 10)
	if err != nil {
		return nbIPAddress{}, false, err
	}
	for _, raw := range raws {
		var item nbIPAddress
		if err := json.Unmarshal(raw, &item); err != nil || item.ID == 0 {
			continue
		}
		address := strings.TrimSpace(item.Address)
		if i := strings.IndexByte(address, '/'); i >= 0 {
			address = address[:i]
		}
		ip := net.ParseIP(address)
		if ip != nil && strings.EqualFold(ip.String(), host) {
			return item, true, nil
		}
	}
	return nbIPAddress{}, false, nil
}

func (c *Client) findInterfaceByMAC(ctx context.Context, mac string) (int, error) {
	// NetBox normalizes MAC addresses, but older data may be stored with
	// either case. Try both spellings before treating it as unmatched.
	id, err := c.findID(ctx, "/api/dcim/interfaces/", url.Values{
		"mac_address": {strings.ToUpper(mac)},
		"limit":       {"1"},
	})
	if err != nil || id > 0 {
		return id, err
	}
	return c.findID(ctx, "/api/dcim/interfaces/", url.Values{
		"mac_address": {strings.ToLower(mac)},
		"limit":       {"1"},
	})
}

func isDuplicateIPError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	for _, marker := range []string{"duplicate", "already exists", "unique"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func usableNetboxIP(ip net.IP) bool {
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