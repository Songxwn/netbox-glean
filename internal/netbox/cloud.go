package netbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// cloudMaxPrefixes bounds the number of NetBox prefixes touched by one sync.
const cloudMaxPrefixes = 1000

type nbPrefix struct {
	ID     int    `json:"id"`
	Prefix string `json:"prefix"`
}

// SyncCloud writes cloud VPC/subnet prefixes and instance or elastic IP
// addresses into NetBox IPAM. Prefixes are created unassigned to a device;
// addresses are created without an interface unless the MAC matches an
// existing NetBox interface.
func SyncCloud(ctx context.Context, settings model.Settings, target model.CloudTarget, result *model.CloudResult, logf model.LogFunc) (*model.SyncResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if !settings.SyncCloudAssets {
		res := &model.SyncResult{}
		res.Warn("云资产同步已在设置中禁用")
		return res, nil
	}
	client, err := NewFromSettings(settings)
	if err != nil {
		return nil, err
	}
	return client.SyncCloud(ctx, settings, target, result, logf)
}

// SyncCloud is the client-level cloud sync implementation.
func (c *Client) SyncCloud(ctx context.Context, settings model.Settings, target model.CloudTarget, result *model.CloudResult, logf model.LogFunc) (*model.SyncResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	res := &model.SyncResult{}

	siteID := 0
	siteSlug := model.First(target.Site, settings.Site)
	if siteSlug != "" {
		id, err := c.ensureSite(ctx, siteSlug, settings.SiteName)
		if err != nil {
			res.Warn("准备 NetBox site %s 失败，云网段将不带 site 写入: %v", siteSlug, err)
		} else {
			siteID = id
		}
	}

	handled := 0
	for _, prefix := range result.Prefixes {
		if ctx.Err() != nil {
			break
		}
		if handled >= cloudMaxPrefixes {
			res.Warn("单次同步最多写入 %d 个云网段，其余已忽略", cloudMaxPrefixes)
			break
		}
		handled++
		description := cloudPrefixDescription(target, prefix)
		created, err := c.ensurePrefix(ctx, prefix.CIDR, description, siteID)
		if err != nil {
			res.Warn("写入网段 %s 失败: %v", prefix.CIDR, err)
			res.PrefixSkipped++
			continue
		}
		if created {
			res.PrefixCreated++
			logf("已创建网段 %s（%s）", prefix.CIDR, description)
		} else {
			res.PrefixSkipped++
		}
	}
	if len(result.Prefixes) > 0 {
		res.Add("云网段: 新建 %d, 已存在/跳过 %d", res.PrefixCreated, res.PrefixSkipped)
	}

	ips := make([]model.IPAddress, 0, len(result.Addresses))
	for _, address := range result.Addresses {
		kind := address.Kind
		if kind == "" {
			kind = model.CloudAddressVM
		}
		ips = append(ips, model.IPAddress{
			Address:   address.Address,
			MAC:       address.MAC,
			Interface: model.First(address.Name, address.InstanceID),
			Source:    "cloud " + target.Provider + " " + kind,
		})
	}
	created, assigned, skipped, warnings := c.syncIPAddresses(ctx, "云资产 "+target.Name, ips, true, logf)
	res.IPCreated = created
	res.IPAssigned = assigned
	res.IPSkipped = skipped
	res.Warnings = append(res.Warnings, warnings...)
	if created+assigned+skipped > 0 {
		res.Add("IP 地址: 新建 %d, 分配 %d, 跳过 %d", created, assigned, skipped)
	}
	return res, nil
}

func cloudPrefixDescription(target model.CloudTarget, prefix model.CloudPrefix) string {
	parts := []string{"netbox-glean", "cloud", model.CloudProviderLabel(target.Provider)}
	if target.Name != "" {
		parts = append(parts, target.Name)
	}
	if prefix.Kind != "" {
		parts = append(parts, prefix.Kind)
	}
	if prefix.Name != "" && prefix.Name != target.Name {
		parts = append(parts, prefix.Name)
	}
	if prefix.VPC != "" && prefix.VPC != prefix.Name {
		parts = append(parts, "vpc="+prefix.VPC)
	}
	if prefix.Region != "" {
		parts = append(parts, prefix.Region)
	}
	return model.Truncate(strings.Join(parts, " "), 200)
}

func (c *Client) ensurePrefix(ctx context.Context, cidr, description string, siteID int) (bool, error) {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return false, fmt.Errorf("网段为空")
	}
	raws, err := c.listAll(ctx, "/api/ipam/prefixes/", url.Values{
		"q":     {cidr},
		"limit": {"20"},
	}, 20)
	if err != nil {
		return false, err
	}
	for _, raw := range raws {
		var item nbPrefix
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.Prefix), cidr) {
			return false, nil
		}
	}
	payload := map[string]any{
		"prefix":      cidr,
		"status":      "active",
		"description": description,
	}
	if siteID > 0 {
		payload["site"] = siteID
	}
	if err := c.do(ctx, http.MethodPost, "/api/ipam/prefixes/", payload, nil); err != nil {
		if isDuplicatePrefixError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func isDuplicatePrefixError(err error) bool {
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
