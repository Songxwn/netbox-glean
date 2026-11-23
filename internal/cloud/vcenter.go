package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

const vcenterWorkers = 8

type vcenterVM struct {
	VM         string `json:"vm"`
	Name       string `json:"name"`
	PowerState string `json:"power_state"`
}

type vcenterGuestInterface struct {
	NIC string `json:"nic"`
	MAC string `json:"mac_address"`
	IP  *struct {
		IPAddresses []string `json:"ip_addresses"`
		Gateway     string   `json:"gateway"`
	} `json:"ip"`
}

func collectVCenter(ctx context.Context, hc *http.Client, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	base := vcenterBase(target.Endpoint)
	if base == "" {
		return nil, fmt.Errorf("vCenter Endpoint 不能为空")
	}
	res := &model.CloudResult{Provider: target.Provider, CollectedAt: time.Now().UTC()}

	sessionID, err := vcenterLogin(ctx, hc, base, target)
	if err != nil {
		return nil, fmt.Errorf("登录 vCenter 失败: %w", err)
	}
	defer vcenterLogout(ctx, hc, base, sessionID)

	if !target.CollectInstances {
		if target.CollectPrefixes {
			res.AddWarning("vCenter API 不提供子网 CIDR，请在“附加网段”中手动填写")
		}
		return res, nil
	}

	vms, err := vcenterListVMs(ctx, hc, base, sessionID)
	if err != nil {
		return nil, fmt.Errorf("查询 vCenter 虚拟机失败: %w", err)
	}
	res.Instances = len(vms)
	logf("vCenter 虚拟机: %d 个", len(vms))

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		failures int
		sem      = make(chan struct{}, vcenterWorkers)
	)
	for _, vm := range vms {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(vm vcenterVM) {
			defer wg.Done()
			defer func() { <-sem }()
			interfaces, err := vcenterGuestInterfaces(ctx, hc, base, sessionID, vm.VM)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures++
				return
			}
			for _, nic := range interfaces {
				if nic.IP == nil {
					continue
				}
				for _, address := range nic.IP.IPAddresses {
					res.Addresses = append(res.Addresses, model.CloudAddress{
						Address:    address,
						Name:       vm.Name,
						InstanceID: vm.VM,
						Interface:  model.First(nic.NIC, nic.MAC),
						MAC:        nic.MAC,
						Kind:       model.CloudAddressVM,
					})
				}
			}
		}(vm)
	}
	wg.Wait()
	if failures > 0 {
		res.AddWarning("%d 台虚拟机无法读取 Guest IP（可能未安装或未运行 VMware Tools）", failures)
	}
	if target.CollectPrefixes {
		res.AddWarning("vCenter API 不提供子网 CIDR，请在“附加网段”中手动填写")
	}
	return res, nil
}

func vcenterBase(value string) string {
	base := cleanBase(value)
	base = strings.TrimSuffix(base, "/api")
	base = strings.TrimSuffix(base, "/sdk")
	return base
}

func vcenterLogin(ctx context.Context, hc *http.Client, base string, target model.CloudTarget) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/session", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(target.AccessKey, target.SecretKey)
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return "", err
	}
	var sessionID string
	if err := json.Unmarshal(data, &sessionID); err != nil || sessionID == "" {
		sessionID = strings.Trim(strings.TrimSpace(string(data)), `"`)
	}
	if sessionID == "" {
		return "", fmt.Errorf("vCenter 未返回会话 ID")
	}
	return sessionID, nil
}

func vcenterLogout(ctx context.Context, hc *http.Client, base, sessionID string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/api/session", nil)
	if err != nil {
		return
	}
	req.Header.Set("vmware-api-session-id", sessionID)
	_, _ = doRequest(context.WithoutCancel(ctx), hc, req)
}

func vcenterGet(ctx context.Context, hc *http.Client, base, sessionID, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("vmware-api-session-id", sessionID)
	req.Header.Set("Accept", "application/json")
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("解析 vCenter 响应失败: %w", err)
		}
	}
	return nil
}

func vcenterListVMs(ctx context.Context, hc *http.Client, base, sessionID string) ([]vcenterVM, error) {
	var vms []vcenterVM
	if err := vcenterGet(ctx, hc, base, sessionID, "/api/vcenter/vm", &vms); err != nil {
		return nil, err
	}
	return vms, nil
}

func vcenterGuestInterfaces(ctx context.Context, hc *http.Client, base, sessionID, vm string) ([]vcenterGuestInterface, error) {
	var interfaces []vcenterGuestInterface
	path := "/api/vcenter/vm/" + url.PathEscape(vm) + "/guest/networking/interfaces"
	if err := vcenterGet(ctx, hc, base, sessionID, path, &interfaces); err != nil {
		return nil, err
	}
	return interfaces, nil
}
