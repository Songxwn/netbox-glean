// Package cloud discovers VPC/subnet ranges and instance or elastic IP
// addresses from cloud APIs. It intentionally implements the small subset of
// each provider API it needs with the standard library so the application
// keeps a minimal dependency footprint.
package cloud

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

const (
	requestTimeout = 90 * time.Second
	maxBodyBytes   = 32 << 20
	maxPages       = 200
	pageSize       = 100
)

// Run performs one cloud asset discovery run.
func Run(ctx context.Context, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := model.ValidateCloudTarget(target); err != nil {
		return nil, err
	}
	region := target.Region
	if region == "" {
		region = "-"
	}
	logf("云平台: %s, 区域: %s", model.CloudProviderLabel(target.Provider), region)
	hc := newHTTPClient(target.Insecure)

	var (
		res *model.CloudResult
		err error
	)
	switch target.Provider {
	case model.CloudVCenter:
		res, err = collectVCenter(ctx, hc, target, logf)
	case model.CloudAliyun:
		res, err = collectAliyun(ctx, hc, target, logf)
	case model.CloudTencent:
		res, err = collectTencent(ctx, hc, target, logf)
	case model.CloudHuawei:
		res, err = collectHuawei(ctx, hc, target, logf)
	case model.CloudAWS:
		res, err = collectAWS(ctx, hc, target, logf)
	case model.CloudGCP:
		res, err = collectGCP(ctx, hc, target, logf)
	case model.CloudVolcengine:
		res, err = collectVolcengine(ctx, hc, target, logf)
	default:
		return nil, fmt.Errorf("不支持的云平台 %q", target.Provider)
	}
	if err != nil {
		return nil, err
	}
	if res == nil {
		res = &model.CloudResult{}
	}
	res.Provider = target.Provider
	if res.CollectedAt.IsZero() {
		res.CollectedAt = time.Now().UTC()
	}
	if target.CollectPrefixes {
		addExtraPrefixes(res, target)
	}
	finalize(res, target)
	logf("云资产采集完成: VPC %d, 子网 %d, 实例 %d, 公网 IP %d, 网段 %d, 地址 %d",
		res.VPCs, res.Subnets, res.Instances, res.PublicIPs, len(res.Prefixes), len(res.Addresses))
	return res, nil
}

func newHTTPClient(insecure bool) *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecure, // #nosec G402 - explicitly opt-in
				MinVersion:         tls.VersionTLS12,
			},
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        20,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 15 * time.Second,
		},
	}
}

func addExtraPrefixes(res *model.CloudResult, target model.CloudTarget) {
	for _, cidr := range target.ExtraCIDRs() {
		res.Prefixes = append(res.Prefixes, model.CloudPrefix{
			CIDR:     cidr,
			Name:     "手动维护",
			Kind:     model.CloudPrefixManual,
			Provider: target.Provider,
		})
	}
}

func finalize(res *model.CloudResult, target model.CloudTarget) {
	prefixes := make([]model.CloudPrefix, 0, len(res.Prefixes))
	seenPrefix := make(map[string]bool, len(res.Prefixes))
	for _, prefix := range res.Prefixes {
		_, network, err := net.ParseCIDR(strings.TrimSpace(prefix.CIDR))
		if err != nil {
			res.AddWarning("忽略无效网段 %q: %v", prefix.CIDR, err)
			continue
		}
		cidr := network.String()
		if seenPrefix[cidr] {
			continue
		}
		seenPrefix[cidr] = true
		prefix.CIDR = cidr
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].CIDR < prefixes[j].CIDR })
	res.Prefixes = prefixes

	addresses := make([]model.CloudAddress, 0, len(res.Addresses))
	index := make(map[string]int, len(res.Addresses))
	for _, entry := range res.Addresses {
		ip := net.ParseIP(strings.TrimSpace(entry.Address))
		if !usableAddress(ip) {
			continue
		}
		address := ip.String()
		if i, ok := index[address]; ok {
			existing := &addresses[i]
			if existing.Name == "" {
				existing.Name = entry.Name
			}
			if existing.InstanceID == "" {
				existing.InstanceID = entry.InstanceID
			}
			if existing.Interface == "" {
				existing.Interface = entry.Interface
			}
			if existing.MAC == "" {
				existing.MAC = entry.MAC
			}
			if existing.Region == "" {
				existing.Region = entry.Region
			}
			if existing.VPC == "" {
				existing.VPC = entry.VPC
			}
			if entry.Public {
				existing.Public = true
				existing.Kind = model.CloudAddressPublic
			}
			continue
		}
		entry.Address = address
		index[address] = len(addresses)
		addresses = append(addresses, entry)
	}
	sort.Slice(addresses, func(i, j int) bool {
		a, b := net.ParseIP(addresses[i].Address), net.ParseIP(addresses[j].Address)
		return bytesLess(a, b)
	})
	res.Addresses = addresses
	res.PublicIPs = 0
	for _, entry := range addresses {
		if entry.Public {
			res.PublicIPs++
		}
	}

	if len(res.Prefixes) == 0 {
		if target.CollectPrefixes {
			res.AddWarning("本次未发现任何 VPC/子网网段")
		}
	}
	if len(res.Addresses) == 0 {
		if target.CollectInstances || target.CollectPublicIPs {
			res.AddWarning("本次未发现任何实例或公网 IP 地址")
		}
	}
}

func bytesLess(a, b net.IP) bool {
	if a == nil || b == nil {
		return a == nil && b != nil
	}
	aa, bb := a.To16(), b.To16()
	for i := 0; i < len(aa) && i < len(bb); i++ {
		if aa[i] != bb[i] {
			return aa[i] < bb[i]
		}
	}
	return false
}

func usableAddress(ip net.IP) bool {
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

// apiError describes a non-2xx cloud API response.
type apiError struct {
	Provider string
	Status   int
	Body     string
}

func (e *apiError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 400 {
		body = body[:400]
	}
	if body == "" {
		return fmt.Sprintf("%s API 返回 HTTP %d", e.Provider, e.Status)
	}
	return fmt.Sprintf("%s API 返回 HTTP %d: %s", e.Provider, e.Status, body)
}

func doRequest(ctx context.Context, hc *http.Client, req *http.Request) ([]byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &apiError{Provider: req.URL.Host, Status: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}

func cleanBase(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func endpointOrDefault(value, fallback string) string {
	if cleanBase(value) != "" {
		return cleanBase(value)
	}
	return fallback
}

func encodeQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for key := range q {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		values := append([]string(nil), q[key]...)
		sort.Strings(values)
		for _, value := range values {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(awsEncode(key))
			b.WriteByte('=')
			b.WriteString(awsEncode(value))
		}
	}
	return b.String()
}
