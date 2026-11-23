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

// nbInterface is the subset of the NetBox interface serializer used for
// cabling decisions.
type nbInterface struct {
	ID         int             `json:"id"`
	Name       string          `json:"name"`
	MACAddress string          `json:"mac_address"`
	Cable      json.RawMessage `json:"cable"`
}

// connected reports whether NetBox already has a cable on this interface.
// NetBox returns "cable": null for free interfaces.
func (i nbInterface) connected() bool {
	value := strings.TrimSpace(string(i.Cable))
	return value != "" && value != "null"
}

// syncCables creates cables between the local device and the LLDP neighbors
// that already exist in NetBox. It never overwrites an existing cable.
func (c *Client) syncCables(ctx context.Context, deviceID int, deviceName, siteSlug string, neighbors []model.Neighbor, logf model.LogFunc) (int, int, []string) {
	var warnings []string
	if len(neighbors) == 0 {
		return 0, 0, warnings
	}
	locals, err := c.deviceInterfaces(ctx, deviceID)
	if err != nil {
		return 0, 0, append(warnings, "读取本端接口失败: "+err.Error())
	}
	localByName := make(map[string]nbInterface, len(locals))
	for _, iface := range locals {
		if iface.Name != "" {
			localByName[strings.ToLower(iface.Name)] = iface
		}
	}

	remoteDevices := map[string]int{}
	remoteInterfaces := map[int][]nbInterface{}
	remoteInterfaceErr := map[int]error{}
	created, skipped, failed := 0, 0, 0
	seen := map[string]bool{}
	warned := map[string]bool{}
	addWarning := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		if warned[message] {
			return
		}
		if len(warnings) >= 25 {
			return
		}
		warned[message] = true
		warnings = append(warnings, message)
	}

	for _, neighbor := range neighbors {
		if ctx.Err() != nil {
			break
		}
		remoteSystem := strings.TrimSpace(neighbor.RemoteSystem)
		localName := strings.TrimSpace(neighbor.LocalInterface)
		if remoteSystem == "" || localName == "" {
			continue
		}
		if strings.EqualFold(remoteSystem, deviceName) {
			continue
		}
		localIface, ok := localByName[strings.ToLower(localName)]
		if !ok {
			addWarning("本端接口 %s 在 NetBox 中不存在，跳过 LLDP 邻居 %s", localName, remoteSystem)
			continue
		}
		if localIface.connected() {
			skipped++
			continue
		}

		key := strings.ToLower(remoteSystem)
		remoteID, cached := remoteDevices[key]
		if !cached {
			remoteID, err = c.findDeviceByName(ctx, remoteSystem, siteSlug)
			if err != nil {
				addWarning("查询远端设备 %s 失败: %v", remoteSystem, err)
				continue
			}
			remoteDevices[key] = remoteID
		}
		if remoteID == 0 {
			addWarning("远端设备 %s 尚未收录到 NetBox，跳过", remoteSystem)
			continue
		}
		if remoteID == deviceID {
			continue
		}

		remoteList, ok := remoteInterfaces[remoteID]
		if !ok {
			if err, seenErr := remoteInterfaceErr[remoteID]; seenErr {
				addWarning("读取远端设备 %s 的接口失败: %v", remoteSystem, err)
				continue
			}
			remoteList, err = c.deviceInterfaces(ctx, remoteID)
			if err != nil {
				remoteInterfaceErr[remoteID] = err
				addWarning("读取远端设备 %s 的接口失败: %v", remoteSystem, err)
				continue
			}
			remoteInterfaces[remoteID] = remoteList
		}

		remoteIface := matchRemoteInterface(remoteList, neighbor.RemotePort, neighbor.RemotePortDesc)
		if remoteIface == nil {
			addWarning("远端设备 %s 上找不到端口 %s", remoteSystem, model.First(neighbor.RemotePort, neighbor.RemotePortDesc, "(未知)"))
			continue
		}
		if remoteIface.connected() {
			skipped++
			continue
		}

		pair := strconv.Itoa(localIface.ID) + "-" + strconv.Itoa(remoteIface.ID)
		reverse := strconv.Itoa(remoteIface.ID) + "-" + strconv.Itoa(localIface.ID)
		if seen[pair] || seen[reverse] {
			skipped++
			continue
		}
		seen[pair] = true

		markCabled := func() {
			localIface.Cable = json.RawMessage(`{"id":0}`)
			localByName[strings.ToLower(localName)] = localIface
			remoteIface.Cable = json.RawMessage(`{"id":0}`)
		}
		if err := c.createCable(ctx, localIface.ID, remoteIface.ID); err != nil {
			if isDuplicateCableError(err) {
				skipped++
				seen[pair] = false
				markCabled()
				continue
			}
			failed++
			addWarning("创建连线 %s <-> %s:%s 失败: %v", localIface.Name, remoteSystem, remoteIface.Name, err)
			continue
		}
		created++
		markCabled()
		logf("已创建连线 %s <-> %s:%s", localIface.Name, remoteSystem, remoteIface.Name)
	}
	if failed > 0 {
		warnings = append(warnings, fmt.Sprintf("有 %d 条连线创建失败", failed))
	}
	return created, skipped, warnings
}

func (c *Client) deviceInterfaces(ctx context.Context, deviceID int) ([]nbInterface, error) {
	raws, err := c.listAll(ctx, "/api/dcim/interfaces/", url.Values{
		"device_id": {strconv.Itoa(deviceID)},
		"limit":     {"1000"},
	}, 5000)
	if err != nil {
		return nil, err
	}
	out := make([]nbInterface, 0, len(raws))
	for _, raw := range raws {
		var iface nbInterface
		if err := json.Unmarshal(raw, &iface); err != nil || iface.ID == 0 {
			continue
		}
		out = append(out, iface)
	}
	return out, nil
}

func (c *Client) findDeviceByName(ctx context.Context, name, siteSlug string) (int, error) {
	queries := []url.Values{}
	if siteSlug != "" {
		queries = append(queries, url.Values{
			"name__ie": {name},
			"site":     {siteSlug},
			"limit":    {"2"},
		})
	}
	queries = append(queries, url.Values{
		"name__ie": {name},
		"limit":    {"2"},
	})
	var lastErr error
	for _, q := range queries {
		id, err := c.findID(ctx, "/api/dcim/devices/", q)
		if err != nil {
			lastErr = err
			continue
		}
		if id > 0 {
			return id, nil
		}
	}
	return 0, lastErr
}

// matchRemoteInterface resolves the LLDP port id against the remote NetBox
// interfaces. LLDP may report the port name, a description or a MAC address.
func matchRemoteInterface(ifaces []nbInterface, port, portDesc string) *nbInterface {
	for _, candidate := range []string{port, portDesc} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		for i := range ifaces {
			if strings.EqualFold(ifaces[i].Name, candidate) {
				return &ifaces[i]
			}
		}
	}
	for _, candidate := range []string{port, portDesc} {
		if mac := normalizeMAC(candidate); mac != "" {
			for i := range ifaces {
				if normalizeMAC(ifaces[i].MACAddress) == mac {
					return &ifaces[i]
				}
			}
		}
	}
	return nil
}

func normalizeMAC(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := net.ParseMAC(value); err == nil {
		return strings.ToLower(parsed.String())
	}
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r == ':' || r == '-' || r == '.':
		default:
			return ""
		}
	}
	if b.Len() != 12 {
		return ""
	}
	hex := b.String()
	return strings.Join([]string{hex[0:2], hex[2:4], hex[4:6], hex[6:8], hex[8:10], hex[10:12]}, ":")
}

func (c *Client) createCable(ctx context.Context, aID, bID int) error {
	payload := map[string]any{
		"a_terminations": []map[string]any{{
			"object_type": "dcim.interface",
			"object_id":   aID,
		}},
		"b_terminations": []map[string]any{{
			"object_type": "dcim.interface",
			"object_id":   bID,
		}},
		"status": "connected",
	}
	err := c.do(ctx, http.MethodPost, "/api/dcim/cables/", payload, nil)
	if err == nil {
		return nil
	}
	// NetBox 3.x expects the legacy termination fields. Retry only for a
	// client error so authentication or server failures are not masked.
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
		legacy := map[string]any{
			"termination_a_type": "dcim.interface",
			"termination_a_id":   aID,
			"termination_b_type": "dcim.interface",
			"termination_b_id":   bID,
			"status":             "connected",
		}
		if legacyErr := c.do(ctx, http.MethodPost, "/api/dcim/cables/", legacy, nil); legacyErr == nil {
			return nil
		}
	}
	return err
}

func isDuplicateCableError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	for _, marker := range []string{"already", "connected", "unique"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}