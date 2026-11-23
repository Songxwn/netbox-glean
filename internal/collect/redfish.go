package collect

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

var errRedfishNotFound = errors.New("redfish resource not found")

type rfClient struct {
	base     string
	user     string
	pass     string
	hc       *http.Client
	logf     model.LogFunc
	warnings []string
}

func newRFClient(target model.Target, logf model.LogFunc) (*rfClient, error) {
	address := strings.TrimSpace(target.Address)
	if address == "" {
		return nil, fmt.Errorf("Redfish 地址为空")
	}
	var base string
	if strings.HasPrefix(address, "https://") || strings.HasPrefix(address, "http://") {
		base = strings.TrimRight(address, "/")
		if strings.HasSuffix(strings.ToLower(base), "/redfish/v1") {
			base = strings.TrimRight(base[:len(base)-len("/redfish/v1")], "/")
		}
	} else {
		port := target.Port
		if port == 0 {
			port = 443
		}
		base = "https://" + net.JoinHostPort(address, strconv.Itoa(port))
	}
	if u, err := url.Parse(base); err != nil || u.Host == "" {
		return nil, fmt.Errorf("无效的 Redfish 地址 %q", base)
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: target.Redfish.Insecure}, // #nosec G402 - explicitly configurable
	}
	return &rfClient{
		base: base,
		user: target.Redfish.Username,
		pass: target.Redfish.Password,
		hc: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		logf: logf,
	}, nil
}

func (c *rfClient) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	c.warnings = append(c.warnings, msg)
	if c.logf != nil {
		c.logf("警告: %s", msg)
	}
}

func (c *rfClient) resolve(ref string) string {
	if ref == "" {
		return c.base + "/redfish/v1"
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	return c.base + "/" + strings.TrimLeft(ref, "/")
}

func (c *rfClient) getJSON(ctx context.Context, ref string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve(ref), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return errRedfishNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return fmt.Errorf("Redfish %s 返回 HTTP %d: %s", req.URL.Path, resp.StatusCode, snippet)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析 Redfish 响应 %s 失败: %w", req.URL.Path, err)
	}
	return nil
}

func (c *rfClient) getMap(ctx context.Context, ref string) (map[string]any, error) {
	m := map[string]any{}
	if err := c.getJSON(ctx, ref, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *rfClient) optionalMap(ctx context.Context, ref string) map[string]any {
	m, err := c.getMap(ctx, ref)
	if err == nil {
		return m
	}
	if !errors.Is(err, errRedfishNotFound) {
		c.warn("%v", err)
	}
	return nil
}

func (c *rfClient) members(ctx context.Context, ref string) ([]string, error) {
	m, err := c.getMap(ctx, ref)
	if err != nil {
		return nil, err
	}
	return memberLinks(m["Members"]), nil
}

func (c *rfClient) optionalMembers(ctx context.Context, ref string) []string {
	refs, err := c.members(ctx, ref)
	if err != nil {
		if !errors.Is(err, errRedfishNotFound) {
			c.warn("读取 %s 失败: %v", ref, err)
		}
		return nil
	}
	return refs
}

func memberLinks(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	links := make([]string, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case string:
			if v != "" {
				links = append(links, v)
			}
		case map[string]any:
			if link := jstr(v, "@odata.id"); link != "" {
				links = append(links, link)
			}
		}
	}
	return links
}

// listFromValue accepts an inline array of objects, a navigation link to a
// collection, or a single linked object and returns resolved objects.
func (c *rfClient) listFromValue(ctx context.Context, value any) []map[string]any {
	var out []map[string]any
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		for _, ref := range c.optionalMembers(ctx, v) {
			if m, err := c.getMap(ctx, ref); err == nil {
				out = append(out, m)
			}
		}
	case map[string]any:
		if link := jstr(v, "@odata.id"); link != "" {
			for _, ref := range c.optionalMembers(ctx, link) {
				if m, err := c.getMap(ctx, ref); err == nil {
					out = append(out, m)
				}
			}
		} else {
			out = append(out, v)
		}
	case []any:
		for _, item := range v {
			switch it := item.(type) {
			case map[string]any:
				if link := jstr(it, "@odata.id"); link != "" {
					if m, err := c.getMap(ctx, link); err == nil {
						out = append(out, m)
					}
				} else {
					out = append(out, it)
				}
			case string:
				if m, err := c.getMap(ctx, it); err == nil {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

func collectRedfish(ctx context.Context, target model.Target, logf model.LogFunc) (*model.Collected, error) {
	client, err := newRFClient(target, logf)
	if err != nil {
		return nil, err
	}
	root, err := client.getMap(ctx, "/redfish/v1")
	if err != nil {
		return nil, fmt.Errorf("连接 Redfish 服务失败: %w", err)
	}
	out := &model.Collected{
		Source:      model.ProtocolRedfish,
		CollectedAt: time.Now().UTC(),
		Extra:       map[string]string{},
	}
	if v := jstr(root, "RedfishVersion"); v != "" {
		out.Extra["redfish_version"] = v
	}
	if v := jstr(root, "Vendor"); v != "" {
		out.Extra["vendor"] = v
	}

	systemRef := ""
	if target.Redfish.SystemID != "" {
		systemRef = "/redfish/v1/Systems/" + url.PathEscape(target.Redfish.SystemID)
	} else {
		refs, err := client.members(ctx, "/redfish/v1/Systems")
		if err != nil {
			return nil, fmt.Errorf("读取 Redfish Systems 失败: %w", err)
		}
		if len(refs) == 0 {
			return nil, fmt.Errorf("Redfish 服务未返回任何 System")
		}
		systemRef = refs[0]
	}
	system, err := client.getMap(ctx, systemRef)
	if err != nil {
		return nil, fmt.Errorf("读取 Redfish System 失败: %w", err)
	}

	id := model.Identity{
		Name:         model.First(target.DeviceName, jstr(system, "HostName"), jstr(system, "Name")),
		Manufacturer: jstr(system, "Manufacturer"),
		Model:        jstr(system, "Model"),
		Serial:       jstr(system, "SerialNumber"),
		AssetTag:     jstr(system, "AssetTag"),
		PowerState:   jstr(system, "PowerState"),
		BIOSVersion:  jstr(system, "BiosVersion"),
		Firmware:     jstr(system, "BiosVersion"),
		UUID:         jstr(system, "UUID"),
	}
	if ps := jmap(system, "ProcessorSummary"); ps != nil {
		id.CPUModel = jstr(ps, "Model")
		if cores := jint(ps, "CoreCount", "TotalCores"); cores > 0 {
			id.CPUCores = int(cores)
		}
	}
	if ms := jmap(system, "MemorySummary"); ms != nil {
		if gib := jfloat(ms, "TotalSystemMemoryGiB"); gib > 0 {
			id.MemoryBytes = int64(gib * 1024 * 1024 * 1024)
		}
	}
	for key, value := range map[string]string{
		"system_id":   jstr(system, "Id"),
		"host_name":   jstr(system, "HostName"),
		"sku":         jstr(system, "SKU"),
		"part_number": jstr(system, "PartNumber"),
		"bios_version": jstr(system, "BiosVersion"),
	} {
		if value != "" {
			out.Extra[key] = value
		}
	}
	logf("Redfish 系统: %s / %s / %s", id.Manufacturer, id.Model, id.Serial)

	totalCores := 0
	processorRefs := client.optionalMembers(ctx, systemRef+"/Processors")
	for index, ref := range processorRefs {
		proc, err := client.getMap(ctx, ref)
		if err != nil {
			client.warn("读取处理器 %s 失败: %v", ref, err)
			continue
		}
		name := model.First(jstr(proc, "Socket"), jstr(proc, "Name"), fmt.Sprintf("CPU %d", index+1))
		cores := int(jint(proc, "TotalCores"))
		totalCores += cores
		if id.CPUModel == "" {
			id.CPUModel = model.First(jstr(proc, "Model"), jstr(proc, "ProcessorType"))
		}
		out.Components = append(out.Components, model.Component{
			Name:         name,
			Class:        "cpu",
			Manufacturer: jstr(proc, "Manufacturer"),
			Model:        jstr(proc, "Model"),
			Serial:       jstr(proc, "SerialNumber"),
			PartNumber:   jstr(proc, "PartNumber"),
			Firmware:     jstr(proc, "FirmwareVersion"),
			Description:  fmt.Sprintf("%d cores / %d threads", cores, jint(proc, "TotalThreads")),
		})
	}
	if totalCores > 0 {
		id.CPUCores = totalCores
	}

	var memoryBytes int64
	memoryRefs := client.optionalMembers(ctx, systemRef+"/Memory")
	for index, ref := range memoryRefs {
		mod, err := client.getMap(ctx, ref)
		if err != nil {
			client.warn("读取内存 %s 失败: %v", ref, err)
			continue
		}
		capacityMiB := jint(mod, "CapacityMiB")
		if capacityMiB > 0 {
			memoryBytes += capacityMiB * 1024 * 1024
		}
		name := model.First(jstr(mod, "DeviceLocator"), jstr(mod, "Name"), fmt.Sprintf("DIMM %d", index+1))
		description := strings.TrimSpace(fmt.Sprintf("%s %d MiB %d MT/s",
			jstr(mod, "MemoryDeviceType"), capacityMiB, jint(mod, "OperatingSpeedMhz")))
		out.Components = append(out.Components, model.Component{
			Name:         name,
			Class:        "memory",
			Manufacturer: jstr(mod, "Manufacturer"),
			Model:        model.First(jstr(mod, "PartNumber"), jstr(mod, "MemoryDeviceType")),
			Serial:       jstr(mod, "SerialNumber"),
			PartNumber:   jstr(mod, "PartNumber"),
			Description:  description,
		})
	}
	if id.MemoryBytes == 0 {
		id.MemoryBytes = memoryBytes
	}

	ethernetRefs := client.optionalMembers(ctx, systemRef+"/EthernetInterfaces")
	for index, ref := range ethernetRefs {
		nic, err := client.getMap(ctx, ref)
		if err != nil {
			client.warn("读取网卡 %s 失败: %v", ref, err)
			continue
		}
		name := model.First(jstr(nic, "Name"), jstr(nic, "Id"), fmt.Sprintf("eth%d", index))
		speed := int(jint(nic, "SpeedMbps"))
		// Most BMCs omit InterfaceEnabled when the port is up, so only
		// treat an explicit false as disabled.
		enabled := true
		if _, ok := nic["InterfaceEnabled"]; ok {
			enabled = jbool(nic, "InterfaceEnabled")
		}
		state := statusState(nic)
		out.Interfaces = append(out.Interfaces, model.Interface{
			Name:        name,
			Description: jstr(nic, "Description"),
			Type:        rfInterfaceType(speed),
			MAC:         sanitizeMAC(jstr(nic, "MACAddress")),
			MTU:         int(jint(nic, "MTU")),
			SpeedMbps:   speed,
			AdminUp:     enabled,
			OperUp:      state == "Enabled" || (state == "" && enabled),
		})
	}

	storageRefs := client.optionalMembers(ctx, systemRef+"/Storage")
	for _, storageRef := range storageRefs {
		storage, err := client.getMap(ctx, storageRef)
		if err != nil {
			client.warn("读取存储 %s 失败: %v", storageRef, err)
			continue
		}
		for _, drive := range client.listFromValue(ctx, storage["Drives"]) {
			name := model.First(jstr(drive, "Name"), jstr(drive, "Id"), jstr(drive, "Model"), "Drive")
			capacity := jint(drive, "CapacityBytes")
			description := strings.TrimSpace(fmt.Sprintf("%s %s %.1f GB",
				jstr(drive, "MediaType"), jstr(drive, "Protocol"), float64(capacity)/(1000*1000*1000)))
			out.Components = append(out.Components, model.Component{
				Name:         name,
				Class:        "disk",
				Manufacturer: jstr(drive, "Manufacturer"),
				Model:        jstr(drive, "Model"),
				Serial:       jstr(drive, "SerialNumber"),
				PartNumber:   jstr(drive, "PartNumber"),
				Firmware:     jstr(drive, "Revision"),
				Description:  description,
			})
		}
	}

	chassisRefs := client.optionalMembers(ctx, "/redfish/v1/Chassis")
	for index, ref := range chassisRefs {
		chassis, err := client.getMap(ctx, ref)
		if err != nil {
			client.warn("读取机箱 %s 失败: %v", ref, err)
			continue
		}
		if id.Manufacturer == "" {
			id.Manufacturer = jstr(chassis, "Manufacturer")
		}
		if id.Model == "" {
			id.Model = jstr(chassis, "Model")
		}
		if id.Serial == "" {
			id.Serial = jstr(chassis, "SerialNumber")
		}
		if id.PowerState == "" {
			id.PowerState = jstr(chassis, "PowerState")
		}
		out.Components = append(out.Components, model.Component{
			Name:         model.First(jstr(chassis, "Name"), fmt.Sprintf("Chassis %d", index+1)),
			Class:        "chassis",
			Manufacturer: jstr(chassis, "Manufacturer"),
			Model:        jstr(chassis, "Model"),
			Serial:       jstr(chassis, "SerialNumber"),
			PartNumber:   jstr(chassis, "PartNumber"),
			Description:  "Redfish chassis",
		})

		if power := client.optionalMap(ctx, ref+"/Power"); power != nil {
			for _, supply := range client.listFromValue(ctx, power["PowerSupplies"]) {
				out.Components = append(out.Components, model.Component{
					Name:         model.First(jstr(supply, "Name"), jstr(supply, "MemberId"), "Power Supply"),
					Class:        "power-supply",
					Manufacturer: jstr(supply, "Manufacturer"),
					Model:        jstr(supply, "Model"),
					Serial:       jstr(supply, "SerialNumber"),
					PartNumber:   jstr(supply, "PartNumber"),
					Firmware:     jstr(supply, "FirmwareVersion"),
					Description:  fmt.Sprintf("%d W", jint(supply, "PowerCapacityWatts")),
				})
			}
		} else if subsystem := client.optionalMap(ctx, ref+"/PowerSubsystem"); subsystem != nil {
			for _, supply := range client.listFromValue(ctx, subsystem["PowerSupplies"]) {
				out.Components = append(out.Components, model.Component{
					Name:         model.First(jstr(supply, "Name"), jstr(supply, "MemberId"), "Power Supply"),
					Class:        "power-supply",
					Manufacturer: jstr(supply, "Manufacturer"),
					Model:        jstr(supply, "Model"),
					Serial:       jstr(supply, "SerialNumber"),
					PartNumber:   jstr(supply, "PartNumber"),
					Firmware:     jstr(supply, "FirmwareVersion"),
					Description:  fmt.Sprintf("%d W", jint(supply, "PowerCapacityWatts", "CapacityWatts")),
				})
			}
		}

		if thermal := client.optionalMap(ctx, ref+"/Thermal"); thermal != nil {
			for _, fan := range client.listFromValue(ctx, thermal["Fans"]) {
				out.Components = append(out.Components, model.Component{
					Name:         model.First(jstr(fan, "Name"), jstr(fan, "MemberId"), "Fan"),
					Class:        "fan",
					Manufacturer: jstr(fan, "Manufacturer"),
					Model:        jstr(fan, "Model"),
					Serial:       jstr(fan, "SerialNumber"),
					PartNumber:   jstr(fan, "PartNumber"),
					Description:  "Redfish fan",
				})
			}
		} else if subsystem := client.optionalMap(ctx, ref+"/ThermalSubsystem"); subsystem != nil {
			for _, fan := range client.listFromValue(ctx, subsystem["Fans"]) {
				out.Components = append(out.Components, model.Component{
					Name:         model.First(jstr(fan, "Name"), jstr(fan, "MemberId"), "Fan"),
					Class:        "fan",
					Manufacturer: jstr(fan, "Manufacturer"),
					Model:        jstr(fan, "Model"),
					Serial:       jstr(fan, "SerialNumber"),
					PartNumber:   jstr(fan, "PartNumber"),
					Description:  "Redfish fan",
				})
			}
		}
	}

	managerRefs := client.optionalMembers(ctx, "/redfish/v1/Managers")
	for index, ref := range managerRefs {
		manager, err := client.getMap(ctx, ref)
		if err != nil {
			client.warn("读取 BMC %s 失败: %v", ref, err)
			continue
		}
		version := model.First(jstr(manager, "FirmwareVersion"), jstr(manager, "Version"))
		if index == 0 && version != "" {
			id.BMCVersion = version
		}
		out.Components = append(out.Components, model.Component{
			Name:         model.First(jstr(manager, "Name"), fmt.Sprintf("BMC %d", index+1)),
			Class:        "bmc",
			Manufacturer: jstr(manager, "Manufacturer"),
			Model:        jstr(manager, "Model"),
			Serial:       jstr(manager, "SerialNumber"),
			Firmware:     version,
			Description:  "Redfish manager",
		})
	}

	firmwareRefs := client.optionalMembers(ctx, "/redfish/v1/UpdateService/FirmwareInventory")
	if len(firmwareRefs) > 200 {
		firmwareRefs = firmwareRefs[:200]
	}
	for _, ref := range firmwareRefs {
		fw, err := client.getMap(ctx, ref)
		if err != nil {
			continue
		}
		name := model.First(jstr(fw, "Name"), jstr(fw, "Id"))
		version := jstr(fw, "Version")
		if name == "" || version == "" {
			continue
		}
		out.Components = append(out.Components, model.Component{
			Name:        name,
			Class:       "firmware",
			Firmware:    version,
			Description: "Firmware inventory",
		})
	}

	out.Identity = id
	out.Warnings = append(out.Warnings, client.warnings...)
	logf("Redfish 采集完成: 组件 %d, 接口 %d", len(out.Components), len(out.Interfaces))
	return out, nil
}

func jstr(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		switch v := m[key].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			return v.String()
		case bool:
			return strconv.FormatBool(v)
		}
	}
	return ""
}

func jint(m map[string]any, keys ...string) int64 {
	if m == nil {
		return 0
	}
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			return int64(v)
		case int:
			return int64(v)
		case int64:
			return v
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return n
			}
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func jfloat(m map[string]any, keys ...string) float64 {
	if m == nil {
		return 0
	}
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case json.Number:
			if f, err := v.Float64(); err == nil {
				return f
			}
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f
			}
		}
	}
	return 0
}

func jbool(m map[string]any, keys ...string) bool {
	if m == nil {
		return false
	}
	for _, key := range keys {
		switch v := m[key].(type) {
		case bool:
			return v
		case string:
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		case float64:
			return v != 0
		}
	}
	return false
}

func jmap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func statusState(m map[string]any) string {
	return jstr(jmap(m, "Status"), "State")
}

func sanitizeMAC(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if mac, err := net.ParseMAC(value); err == nil {
		return strings.ToLower(mac.String())
	}
	return ""
}

func rfInterfaceType(speedMbps int) string {
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