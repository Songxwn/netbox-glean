package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

const pveWorkers = 8

// pveGuest is one QEMU VM or LXC container from /cluster/resources.
type pveGuest struct {
	Node     string `json:"node"`
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Type     string `json:"type"` // qemu or lxc
	Template int    `json:"template"`
}

type pveAgentInterface struct {
	Name        string `json:"name"`
	MAC         string `json:"hardware-address"`
	IPAddresses []struct {
		Type    string `json:"ip-address-type"`
		Address string `json:"ip-address"`
		Prefix  int    `json:"prefix"`
	} `json:"ip-addresses"`
}

type pveLXCInterface struct {
	Name  string `json:"name"`
	MAC   string `json:"hwaddr"`
	Inet  string `json:"inet"`
	Inet6 string `json:"inet6"`
}

// pveAuth carries either an API token header or a login ticket cookie.
type pveAuth struct {
	header string
	cookie string
}

func (a pveAuth) apply(req *http.Request) {
	if a.header != "" {
		req.Header.Set("Authorization", a.header)
	}
	if a.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: a.cookie})
	}
}

func collectPVE(ctx context.Context, hc *http.Client, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	base := pveBase(target.Endpoint)
	if base == "" {
		return nil, fmt.Errorf("Proxmox VE Endpoint 不能为空")
	}
	res := &model.CloudResult{Provider: target.Provider, CollectedAt: time.Now().UTC()}

	auth, err := pveLogin(ctx, hc, base, target)
	if err != nil {
		return nil, fmt.Errorf("登录 Proxmox VE 失败: %w", err)
	}

	if target.CollectPublicIPs {
		res.AddWarning("Proxmox VE 没有弹性公网 IP 概念，已忽略该选项")
	}
	if !target.CollectInstances {
		return res, nil
	}

	guests, err := pveListGuests(ctx, hc, base, auth)
	if err != nil {
		return nil, fmt.Errorf("查询 Proxmox VE 虚拟机/容器失败: %w", err)
	}
	res.Instances = len(guests)
	logf("Proxmox VE 虚拟机/容器: %d 个", len(guests))

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		failures int
		sem      = make(chan struct{}, pveWorkers)
	)
	for _, guest := range guests {
		if ctx.Err() != nil {
			break
		}
		if guest.Status != "running" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(guest pveGuest) {
			defer wg.Done()
			defer func() { <-sem }()
			addresses, prefixes, err := pveGuestAddresses(ctx, hc, base, auth, guest, target.CollectPrefixes)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures++
				return
			}
			res.Addresses = append(res.Addresses, addresses...)
			res.Prefixes = append(res.Prefixes, prefixes...)
		}(guest)
	}
	wg.Wait()
	if failures > 0 {
		res.AddWarning("%d 台虚拟机/容器无法读取 Guest IP（QEMU 虚拟机需安装并运行 qemu-guest-agent）", failures)
	}
	return res, nil
}

// pveBase normalizes the endpoint to the /api2/json API root. Users may paste
// the bare host URL or the full API path.
func pveBase(value string) string {
	base := cleanBase(value)
	base = strings.TrimSuffix(base, "/api2/json")
	base = strings.TrimSuffix(base, "/api2")
	if base == "" {
		return ""
	}
	return base + "/api2/json"
}

// pveLogin uses API token authentication when the user name contains a token
// id ("user@realm!tokenid"), otherwise it logs in with a ticket.
func pveLogin(ctx context.Context, hc *http.Client, base string, target model.CloudTarget) (pveAuth, error) {
	if strings.Contains(target.AccessKey, "!") {
		return pveAuth{header: "PVEAPIToken=" + target.AccessKey + "=" + target.SecretKey}, nil
	}
	form := url.Values{"username": {target.AccessKey}, "password": {target.SecretKey}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/access/ticket", strings.NewReader(form.Encode()))
	if err != nil {
		return pveAuth{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return pveAuth{}, err
	}
	var payload struct {
		Data struct {
			Ticket string `json:"ticket"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return pveAuth{}, fmt.Errorf("解析 Proxmox VE 登录响应失败: %w", err)
	}
	if payload.Data.Ticket == "" {
		return pveAuth{}, fmt.Errorf("Proxmox VE 未返回登录 ticket")
	}
	return pveAuth{cookie: url.QueryEscape(payload.Data.Ticket)}, nil
}

func pveGet(ctx context.Context, hc *http.Client, base string, auth pveAuth, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	auth.apply(req)
	req.Header.Set("Accept", "application/json")
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return err
	}
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("解析 Proxmox VE 响应失败: %w", err)
	}
	if out != nil && len(payload.Data) > 0 {
		if err := json.Unmarshal(payload.Data, out); err != nil {
			return fmt.Errorf("解析 Proxmox VE 响应失败: %w", err)
		}
	}
	return nil
}

func pveListGuests(ctx context.Context, hc *http.Client, base string, auth pveAuth) ([]pveGuest, error) {
	var guests []pveGuest
	if err := pveGet(ctx, hc, base, auth, "/cluster/resources?type=vm", &guests); err != nil {
		return nil, err
	}
	filtered := guests[:0]
	for _, guest := range guests {
		if guest.Template != 0 || guest.Node == "" || guest.VMID == 0 {
			continue
		}
		if guest.Type != "qemu" && guest.Type != "lxc" {
			continue
		}
		filtered = append(filtered, guest)
	}
	return filtered, nil
}

// pveGuestAddresses reads the guest NIC addresses of one running VM or
// container. QEMU VMs require qemu-guest-agent; LXC interfaces are read from
// the container configuration directly.
func pveGuestAddresses(ctx context.Context, hc *http.Client, base string, auth pveAuth, guest pveGuest, collectPrefixes bool) ([]model.CloudAddress, []model.CloudPrefix, error) {
	path := "/nodes/" + url.PathEscape(guest.Node) + "/" + guest.Type + "/" + strconv.Itoa(guest.VMID)
	var (
		addresses []model.CloudAddress
		prefixes  []model.CloudPrefix
	)
	appendIP := func(ip string, prefixLen int, iface, mac string) {
		address, prefix, ok := pveAddress(ip, prefixLen, guest, iface, mac, collectPrefixes)
		if !ok {
			return
		}
		addresses = append(addresses, address)
		if prefix.CIDR != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	if guest.Type == "qemu" {
		var payload struct {
			Result []pveAgentInterface `json:"result"`
		}
		if err := pveGet(ctx, hc, base, auth, path+"/agent/network-get-interfaces", &payload); err != nil {
			return nil, nil, err
		}
		for _, nic := range payload.Result {
			if strings.EqualFold(nic.Name, "lo") {
				continue
			}
			for _, entry := range nic.IPAddresses {
				appendIP(entry.Address, entry.Prefix, nic.Name, nic.MAC)
			}
		}
		return addresses, prefixes, nil
	}
	var interfaces []pveLXCInterface
	if err := pveGet(ctx, hc, base, auth, path+"/interfaces", &interfaces); err != nil {
		return nil, nil, err
	}
	for _, nic := range interfaces {
		if strings.EqualFold(nic.Name, "lo") {
			continue
		}
		for _, cidr := range []string{nic.Inet, nic.Inet6} {
			ip, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
			if err != nil {
				continue
			}
			ones, _ := network.Mask.Size()
			appendIP(ip.String(), ones, nic.Name, nic.MAC)
		}
	}
	return addresses, prefixes, nil
}

// pveAddress converts one guest IP into a cloud address and, when the prefix
// length describes a real subnet (not a host route), the subnet prefix.
func pveAddress(ip string, prefixLen int, guest pveGuest, iface, mac string, withPrefix bool) (model.CloudAddress, model.CloudPrefix, bool) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if !usableAddress(parsed) {
		return model.CloudAddress{}, model.CloudPrefix{}, false
	}
	address := model.CloudAddress{
		Address:    parsed.String(),
		Name:       guest.Name,
		InstanceID: strconv.Itoa(guest.VMID),
		Interface:  model.First(iface, mac),
		MAC:        mac,
		Kind:       model.CloudAddressVM,
	}
	var prefix model.CloudPrefix
	hostBits := 32
	if parsed.To4() == nil {
		hostBits = 128
	}
	if withPrefix && prefixLen > 0 && prefixLen < hostBits {
		if _, network, err := net.ParseCIDR(parsed.String() + "/" + strconv.Itoa(prefixLen)); err == nil {
			prefix = model.CloudPrefix{
				CIDR:     network.String(),
				Kind:     model.CloudPrefixSubnet,
				Provider: model.CloudPVE,
			}
		}
	}
	return address, prefix, true
}
