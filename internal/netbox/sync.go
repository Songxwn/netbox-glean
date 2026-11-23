package netbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// Sync writes a collected inventory into NetBox and reports what changed.
func Sync(ctx context.Context, settings model.Settings, target model.Target, data *model.Collected, logf model.LogFunc) (*model.SyncResult, error) {
	if data == nil {
		return nil, fmt.Errorf("没有可同步的采集结果")
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	client, err := NewFromSettings(settings)
	if err != nil {
		return nil, err
	}

	siteSlug := model.First(target.Site, settings.Site)
	if siteSlug == "" {
		return nil, fmt.Errorf("未配置 NetBox site，可在设置或目标中填写 site slug")
	}
	siteName := titleize(siteSlug)
	if target.Site == "" || strings.EqualFold(slugify(target.Site), slugify(settings.Site)) {
		siteName = model.First(settings.SiteName, siteName)
	}
	roleSlug := model.First(target.DeviceRole, settings.DeviceRole, "server")
	platformSlug := model.First(target.Platform, settings.Platform)
	deviceName := model.First(target.DeviceName, data.Identity.Name, target.Name)
	manufacturer := model.First(data.Identity.Manufacturer, "Unknown")
	deviceModel := model.First(data.Identity.Model, "Unknown Model")
	uHeight := settings.UHeight
	if uHeight <= 0 {
		uHeight = 1
	}

	res := &model.SyncResult{DeviceName: deviceName}
	logf("同步到 NetBox: %s (%s / %s)", deviceName, manufacturer, deviceModel)

	customFields := map[string]any{}
	if settings.CustomFields {
		names, err := client.customFieldNames(ctx)
		if err != nil {
			res.Warn("读取 NetBox 自定义字段失败，跳过 custom_fields: %v", err)
			logf("读取 custom_fields 失败: %v", err)
		} else {
			putString := func(field, value string) {
				if value != "" {
					if _, ok := names[field]; ok {
						customFields[field] = value
					}
				}
			}
			putString("glean_last_seen", data.CollectedAt.UTC().Format(time.RFC3339))
			putString("glean_source", data.Source)
			putString("glean_power_state", data.Identity.PowerState)
			putString("glean_cpu_model", data.Identity.CPUModel)
			putString("glean_bios_version", model.First(data.Identity.BIOSVersion, data.Identity.Firmware))
			putString("glean_bmc_version", data.Identity.BMCVersion)
			putString("glean_uuid", data.Identity.UUID)
			putString("glean_manufacturer", data.Identity.Manufacturer)
			putString("glean_model", data.Identity.Model)
			putString("glean_serial", data.Identity.Serial)
			putString("glean_managed_by", "netbox-glean")
			if data.Identity.CPUCores > 0 {
				if _, ok := names["glean_cpu_cores"]; ok {
					customFields["glean_cpu_cores"] = data.Identity.CPUCores
				}
			}
			if data.Identity.MemoryGB > 0 {
				if _, ok := names["glean_memory_gb"]; ok {
					customFields["glean_memory_gb"] = data.Identity.MemoryGB
				}
			}
		}
	}

	deviceID, created, err := client.ensureDevice(ctx, deviceRequest{
		Name:         deviceName,
		SiteSlug:     siteSlug,
		SiteName:     siteName,
		RoleSlug:     roleSlug,
		PlatformSlug: platformSlug,
		Manufacturer: manufacturer,
		Model:        deviceModel,
		Serial:       data.Identity.Serial,
		AssetTag:     data.Identity.AssetTag,
		Description:  settings.Description,
		UHeight:      uHeight,
		CustomFields: customFields,
	})
	if err != nil {
		return nil, err
	}
	res.DeviceID = deviceID
	res.Created = created
	res.DeviceURL = client.base + "/dcim/devices/" + strconv.Itoa(deviceID) + "/"
	if created {
		res.Add("创建 NetBox 设备 %s", deviceName)
		logf("已创建 NetBox 设备 #%d", deviceID)
	} else {
		res.Add("更新 NetBox 设备 %s", deviceName)
		logf("已更新 NetBox 设备 #%d", deviceID)
	}

	if settings.SyncInterfaces && len(data.Interfaces) > 0 {
		createdCount, updatedCount, warnings := client.syncInterfaces(ctx, deviceID, data.Interfaces, settings.PruneInterfaces, settings.PrimaryInterface, logf)
		res.InterfacesCreated = createdCount
		res.InterfacesUpdated = updatedCount
		res.Warnings = append(res.Warnings, warnings...)
		if createdCount+updatedCount > 0 {
			res.Add("同步接口: 新建 %d, 更新 %d", createdCount, updatedCount)
		}
	}

	if settings.SyncInventory && len(data.Components) > 0 {
		createdCount, updatedCount, warnings := client.syncInventory(ctx, deviceID, data.Components, logf)
		res.InventoryCreated = createdCount
		res.InventoryUpdated = updatedCount
		res.Warnings = append(res.Warnings, warnings...)
		if createdCount+updatedCount > 0 {
			res.Add("同步硬件清单: 新建 %d, 更新 %d", createdCount, updatedCount)
		}
	}

	if settings.AssignPrimaryIP {
		if ip := managementIP(target.Address); ip != "" {
			cidr, warnings, err := client.assignPrimaryIP(ctx, deviceID, ip, model.First(settings.PrimaryInterface, "mgmt0"), logf)
			res.Warnings = append(res.Warnings, warnings...)
			if err != nil {
				res.Warn("分配主 IP 失败: %v", err)
			} else {
				res.IPAddress = cidr
				res.Add("设置主 IP: %s", cidr)
			}
		}
	}

	logf("NetBox 同步完成: 设备 #%d", deviceID)
	return res, nil
}

type deviceRequest struct {
	Name         string
	SiteSlug     string
	SiteName     string
	RoleSlug     string
	PlatformSlug string
	Manufacturer string
	Model        string
	Serial       string
	AssetTag     string
	Description  string
	UHeight      int
	CustomFields map[string]any
}

func (c *Client) ensureDevice(ctx context.Context, req deviceRequest) (int, bool, error) {
	siteID, err := c.ensureSite(ctx, req.SiteSlug, req.SiteName)
	if err != nil {
		return 0, false, fmt.Errorf("准备 site 失败: %w", err)
	}
	roleID, err := c.ensureRole(ctx, req.RoleSlug)
	if err != nil {
		return 0, false, fmt.Errorf("准备 device role 失败: %w", err)
	}
	platformID := 0
	if req.PlatformSlug != "" {
		platformID, err = c.ensurePlatform(ctx, req.PlatformSlug)
		if err != nil {
			return 0, false, fmt.Errorf("准备 platform 失败: %w", err)
		}
	}
	manufacturerID, err := c.ensureManufacturer(ctx, req.Manufacturer)
	if err != nil {
		return 0, false, fmt.Errorf("准备 manufacturer 失败: %w", err)
	}
	deviceTypeID, err := c.ensureDeviceType(ctx, manufacturerID, req.Manufacturer, req.Model, req.UHeight)
	if err != nil {
		return 0, false, fmt.Errorf("准备 device type 失败: %w", err)
	}

	deviceID, err := c.findDevice(ctx, req.Name, req.Serial, siteID)
	if err != nil {
		return 0, false, err
	}
	if deviceID == 0 {
		payload := map[string]any{
			"name":        model.Truncate(req.Name, 64),
			"device_type": deviceTypeID,
			"role":        roleID,
			"site":        siteID,
			"status":      "active",
		}
		if serial := model.Truncate(req.Serial, 50); serial != "" {
			payload["serial"] = serial
		}
		if asset := model.Truncate(req.AssetTag, 50); asset != "" {
			payload["asset_tag"] = asset
		}
		if platformID > 0 {
			payload["platform"] = platformID
		}
		if req.Description != "" {
			payload["description"] = model.Truncate(req.Description, 200)
		}
		if len(req.CustomFields) > 0 {
			payload["custom_fields"] = req.CustomFields
		}
		var created struct {
			ID int `json:"id"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/dcim/devices/", payload, &created); err != nil {
			return 0, false, err
		}
		if created.ID == 0 {
			return 0, false, fmt.Errorf("NetBox 创建设备后未返回 ID")
		}
		return created.ID, true, nil
	}

	patch := map[string]any{
		"name":        model.Truncate(req.Name, 64),
		"device_type": deviceTypeID,
		"role":        roleID,
		"site":        siteID,
	}
	if serial := model.Truncate(req.Serial, 50); serial != "" {
		patch["serial"] = serial
	}
	if asset := model.Truncate(req.AssetTag, 50); asset != "" {
		patch["asset_tag"] = asset
	}
	if platformID > 0 {
		patch["platform"] = platformID
	}
	if req.Description != "" {
		patch["description"] = model.Truncate(req.Description, 200)
	}
	if len(req.CustomFields) > 0 {
		patch["custom_fields"] = req.CustomFields
	}
	if len(patch) > 0 {
		if err := c.do(ctx, http.MethodPatch, "/api/dcim/devices/"+strconv.Itoa(deviceID)+"/", patch, nil); err != nil {
			return 0, false, err
		}
	}
	return deviceID, false, nil
}

func (c *Client) findDevice(ctx context.Context, name, serial string, siteID int) (int, error) {
	site := strconv.Itoa(siteID)
	if name != "" {
		id, err := c.findID(ctx, "/api/dcim/devices/", url.Values{
			"name":    {name},
			"site_id": {site},
			"limit":   {"1"},
		})
		if err != nil {
			return 0, fmt.Errorf("查询设备失败: %w", err)
		}
		if id > 0 {
			return id, nil
		}
	}
	if serial != "" {
		id, err := c.findID(ctx, "/api/dcim/devices/", url.Values{
			"serial":  {serial},
			"site_id": {site},
			"limit":   {"1"},
		})
		if err != nil {
			return 0, fmt.Errorf("按序列号查询设备失败: %w", err)
		}
		if id > 0 {
			return id, nil
		}
	}
	return 0, nil
}

func (c *Client) ensureBySlug(ctx context.Context, listPath, createPath, slug, name string, extra map[string]any) (int, error) {
	if slug == "" {
		return 0, fmt.Errorf("slug 不能为空")
	}
	id, err := c.findID(ctx, listPath, url.Values{"slug": {slug}, "limit": {"1"}})
	if err != nil {
		return 0, err
	}
	if id > 0 {
		return id, nil
	}
	payload := map[string]any{"name": name, "slug": slug}
	for key, value := range extra {
		payload[key] = value
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, createPath, payload, &created); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
			if existing, findErr := c.findID(ctx, listPath, url.Values{"slug": {slug}, "limit": {"1"}}); findErr == nil && existing > 0 {
				return existing, nil
			}
		}
		return 0, err
	}
	if created.ID == 0 {
		return 0, fmt.Errorf("创建 %s 后未返回 ID", name)
	}
	return created.ID, nil
}

func (c *Client) ensureManufacturer(ctx context.Context, name string) (int, error) {
	name = model.First(name, "Unknown")
	slug := slugify(name)
	c.mu.Lock()
	if id, ok := c.manufacturers[slug]; ok {
		c.mu.Unlock()
		return id, nil
	}
	c.mu.Unlock()
	id, err := c.ensureBySlug(ctx, "/api/dcim/manufacturers/", "/api/dcim/manufacturers/", slug, model.Truncate(name, 100), nil)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.manufacturers[slug] = id
	c.mu.Unlock()
	return id, nil
}

func (c *Client) ensureSite(ctx context.Context, slug, name string) (int, error) {
	slug = slugify(slug)
	if slug == "" {
		return 0, fmt.Errorf("site slug 不能为空")
	}
	name = model.Truncate(model.First(name, titleize(slug)), 100)
	return c.ensureBySlug(ctx, "/api/dcim/sites/", "/api/dcim/sites/", slug, name, map[string]any{"status": "active"})
}

func (c *Client) ensureRole(ctx context.Context, slug string) (int, error) {
	slug = slugify(slug)
	if slug == "" {
		slug = "server"
	}
	return c.ensureBySlug(ctx, "/api/dcim/device-roles/", "/api/dcim/device-roles/", slug, titleize(slug), map[string]any{
		"color":   "9e9e9e",
		"vm_role": false,
	})
}

func (c *Client) ensurePlatform(ctx context.Context, slug string) (int, error) {
	slug = slugify(slug)
	if slug == "" {
		return 0, nil
	}
	return c.ensureBySlug(ctx, "/api/dcim/platforms/", "/api/dcim/platforms/", slug, titleize(slug), nil)
}

func (c *Client) ensureDeviceType(ctx context.Context, manufacturerID int, manufacturer, deviceModel string, uHeight int) (int, error) {
	deviceModel = model.Truncate(model.First(deviceModel, "Unknown Model"), 100)
	q := url.Values{
		"manufacturer_id": {strconv.Itoa(manufacturerID)},
		"model":           {deviceModel},
		"limit":           {"1"},
	}
	id, err := c.findID(ctx, "/api/dcim/device-types/", q)
	if err != nil {
		return 0, err
	}
	if id > 0 {
		return id, nil
	}
	slug := slugify(manufacturer + "-" + deviceModel)
	payload := map[string]any{
		"manufacturer": manufacturerID,
		"model":        deviceModel,
		"slug":         slug,
		"u_height":     uHeight,
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/dcim/device-types/", payload, &created); err != nil {
		if existing, findErr := c.findID(ctx, "/api/dcim/device-types/", q); findErr == nil && existing > 0 {
			return existing, nil
		}
		return 0, err
	}
	if created.ID == 0 {
		return 0, fmt.Errorf("创建 device type %s 后未返回 ID", deviceModel)
	}
	return created.ID, nil
}

func (c *Client) syncInterfaces(ctx context.Context, deviceID int, ifaces []model.Interface, prune bool, protected string, logf model.LogFunc) (int, int, []string) {
	var warnings []string
	raws, err := c.listAll(ctx, "/api/dcim/interfaces/", url.Values{
		"device_id": {strconv.Itoa(deviceID)},
		"limit":     {"1000"},
	}, 5000)
	if err != nil {
		return 0, 0, append(warnings, "读取现有接口失败: "+err.Error())
	}
	type existing struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	byName := map[string]int{}
	for _, raw := range raws {
		var item existing
		if err := jsonUnmarshal(raw, &item); err == nil && item.Name != "" {
			byName[strings.ToLower(item.Name)] = item.ID
		}
	}
	seen := map[string]bool{}
	createdCount, updatedCount := 0, 0
	for _, iface := range ifaces {
		name := model.Truncate(iface.Name, 64)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		seen[key] = true
		payload := map[string]any{
			"name":    name,
			"type":    model.First(iface.Type, "other"),
			"enabled": iface.AdminUp,
		}
		if iface.Description != "" {
			payload["description"] = model.Truncate(iface.Description, 200)
		}
		if iface.MTU > 0 {
			payload["mtu"] = iface.MTU
		}
		if iface.MAC != "" {
			payload["mac_address"] = iface.MAC
		}
		if iface.SpeedMbps > 0 {
			payload["speed"] = iface.SpeedMbps * 1000
		}
		if id := byName[key]; id > 0 {
			if err := c.do(ctx, http.MethodPatch, "/api/dcim/interfaces/"+strconv.Itoa(id)+"/", payload, nil); err != nil {
				warnings = append(warnings, fmt.Sprintf("更新接口 %s 失败: %v", name, err))
				continue
			}
			updatedCount++
			continue
		}
		payload["device"] = deviceID
		var created struct {
			ID int `json:"id"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/dcim/interfaces/", payload, &created); err != nil {
			warnings = append(warnings, fmt.Sprintf("创建接口 %s 失败: %v", name, err))
			continue
		}
		createdCount++
	}
	if prune {
		for existingName, id := range byName {
			if seen[existingName] || existingName == strings.ToLower(protected) {
				continue
			}
			if err := c.do(ctx, http.MethodDelete, "/api/dcim/interfaces/"+strconv.Itoa(id)+"/", nil, nil); err != nil {
				warnings = append(warnings, fmt.Sprintf("删除过期接口 %s 失败: %v", existingName, err))
			} else {
				logf("已删除过期接口 %s", existingName)
			}
		}
	}
	return createdCount, updatedCount, warnings
}

func (c *Client) syncInventory(ctx context.Context, deviceID int, components []model.Component, logf model.LogFunc) (int, int, []string) {
	var warnings []string
	raws, err := c.listAll(ctx, "/api/dcim/inventory-items/", url.Values{
		"device_id": {strconv.Itoa(deviceID)},
		"limit":     {"1000"},
	}, 10000)
	if err != nil {
		return 0, 0, append(warnings, "读取现有硬件清单失败: "+err.Error())
	}
	type existing struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	byName := map[string]int{}
	for _, raw := range raws {
		var item existing
		if err := jsonUnmarshal(raw, &item); err == nil && item.Name != "" {
			byName[strings.ToLower(item.Name)] = item.ID
		}
	}
	cache := map[string]int{}
	createdCount, updatedCount := 0, 0
	for _, comp := range components {
		name := model.Truncate(comp.Name, 64)
		if name == "" {
			continue
		}
		payload := map[string]any{
			"name":        name,
			"label":       model.Truncate(model.First(comp.Model, comp.Class), 64),
			"status":      "active",
			"discovered":  true,
			"description": model.Truncate(model.First(comp.Description, "class: "+comp.Class), 200),
		}
		if part := model.Truncate(comp.PartNumber, 50); part != "" {
			payload["part_id"] = part
		}
		if serial := model.Truncate(comp.Serial, 50); serial != "" {
			payload["serial"] = serial
		}
		if asset := model.Truncate(comp.AssetTag, 50); asset != "" {
			payload["asset_tag"] = asset
		}
		if comp.Manufacturer != "" {
			slug := slugify(comp.Manufacturer)
			if id, ok := cache[slug]; ok {
				if id > 0 {
					payload["manufacturer"] = id
				}
			} else {
				id, mfrErr := c.ensureManufacturer(ctx, comp.Manufacturer)
				if mfrErr != nil {
					warnings = append(warnings, fmt.Sprintf("准备组件厂商 %s 失败: %v", comp.Manufacturer, mfrErr))
					cache[slug] = 0
				} else {
					cache[slug] = id
					payload["manufacturer"] = id
				}
			}
		}
		if id := byName[strings.ToLower(name)]; id > 0 {
			if err := c.do(ctx, http.MethodPatch, "/api/dcim/inventory-items/"+strconv.Itoa(id)+"/", payload, nil); err != nil {
				warnings = append(warnings, fmt.Sprintf("更新组件 %s 失败: %v", name, err))
				continue
			}
			updatedCount++
			continue
		}
		payload["device"] = deviceID
		var created struct {
			ID int `json:"id"`
		}
		if err := c.do(ctx, http.MethodPost, "/api/dcim/inventory-items/", payload, &created); err != nil {
			warnings = append(warnings, fmt.Sprintf("创建组件 %s 失败: %v", name, err))
			continue
		}
		createdCount++
	}
	logf("硬件清单同步: 新建 %d, 更新 %d", createdCount, updatedCount)
	return createdCount, updatedCount, warnings
}

func (c *Client) customFieldNames(ctx context.Context) (map[string]bool, error) {
	raws, err := c.listAll(ctx, "/api/extras/custom-fields/", url.Values{"limit": {"500"}}, 2000)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, raw := range raws {
		var field struct {
			Name         string   `json:"name"`
			ContentTypes []string `json:"content_types"`
		}
		if err := jsonUnmarshal(raw, &field); err != nil || field.Name == "" {
			continue
		}
		// Only use fields that may be written on devices. Older NetBox
		// releases may omit content_types, in which case we keep the field.
		if len(field.ContentTypes) > 0 && !containsString(field.ContentTypes, "dcim.device") {
			continue
		}
		names[field.Name] = true
	}
	return names, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (c *Client) assignPrimaryIP(ctx context.Context, deviceID int, address, ifaceName string, logf model.LogFunc) (string, []string, error) {
	var warnings []string
	ip := net.ParseIP(strings.Trim(address, "[]"))
	if ip == nil {
		return "", nil, fmt.Errorf("不是有效的 IP 地址: %s", address)
	}
	ifaceName = model.First(ifaceName, "mgmt0")
	ifaceID, err := c.findID(ctx, "/api/dcim/interfaces/", url.Values{
		"device_id": {strconv.Itoa(deviceID)},
		"name":      {ifaceName},
		"limit":     {"1"},
	})
	if err != nil {
		return "", warnings, err
	}
	if ifaceID == 0 {
		var created struct {
			ID int `json:"id"`
		}
		err := c.do(ctx, http.MethodPost, "/api/dcim/interfaces/", map[string]any{
			"device":      deviceID,
			"name":        ifaceName,
			"type":        "1000base-t",
			"mgmt_only":   true,
			"enabled":     true,
			"description": "netbox-glean management interface",
		}, &created)
		if err != nil {
			return "", warnings, fmt.Errorf("创建管理接口 %s 失败: %w", ifaceName, err)
		}
		ifaceID = created.ID
	}

	bits := 32
	field := "primary_ip4"
	if ip.To4() == nil {
		bits = 128
		field = "primary_ip6"
	}
	cidr := fmt.Sprintf("%s/%d", ip.String(), bits)
	ipID, err := c.findID(ctx, "/api/ipam/ip-addresses/", url.Values{
		"address": {cidr},
		"limit":   {"1"},
	})
	if err != nil {
		return "", warnings, err
	}
	if ipID == 0 {
		var created struct {
			ID int `json:"id"`
		}
		err := c.do(ctx, http.MethodPost, "/api/ipam/ip-addresses/", map[string]any{
			"address":              cidr,
			"status":               "active",
			"assigned_object_type": "dcim.interface",
			"assigned_object_id":   ifaceID,
			"description":          "netbox-glean discovered address",
		}, &created)
		if err != nil {
			return "", warnings, fmt.Errorf("创建 IP 地址 %s 失败: %w", cidr, err)
		}
		ipID = created.ID
	}
	if err := c.do(ctx, http.MethodPatch, "/api/dcim/devices/"+strconv.Itoa(deviceID)+"/", map[string]any{field: ipID}, nil); err != nil {
		return "", warnings, fmt.Errorf("设置 %s 失败: %w", field, err)
	}
	logf("已设置设备主 IP %s", cidr)
	return cidr, warnings, nil
}

func managementIP(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return ""
	}
	// Redfish targets may be stored as a full URL. Use the host part so the
	// BMC address can still be assigned as the primary IP.
	if strings.Contains(address, "://") {
		u, err := url.Parse(address)
		if err != nil {
			return ""
		}
		address = u.Hostname()
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	address = strings.Trim(address, "[]")
	if net.ParseIP(address) == nil {
		return ""
	}
	return address
}

func jsonUnmarshal(data []byte, out any) error {
	return json.Unmarshal(data, out)
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.' || r == '/':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "item-" + shortHash(value)
	}
	if len(out) > 100 {
		out = strings.Trim(out[:100], "-")
	}
	return out
}

func shortHash(value string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	out := strconv.FormatUint(h.Sum64(), 16)
	if len(out) < 8 {
		out = strings.Repeat("0", 8-len(out)) + out
	}
	return out[:8]
}

func titleize(slug string) string {
	parts := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' })
	for i, part := range parts {
		r := []rune(part)
		if len(r) == 0 {
			continue
		}
		r[0] = unicode.ToUpper(r[0])
		parts[i] = string(r)
	}
	return strings.Join(parts, " ")
}