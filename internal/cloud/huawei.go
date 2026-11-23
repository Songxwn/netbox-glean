package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

type huaweiVPC struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Cidr   string `json:"cidr"`
	Status string `json:"status"`
}

type huaweiSubnet struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Cidr      string `json:"cidr"`
	VpcID     string `json:"vpc_id"`
	Status    string `json:"status"`
	NetworkID string `json:"neutron_network_id"`
}

type huaweiPublicIP struct {
	ID               string `json:"id"`
	PublicIPAddress  string `json:"public_ip_address"`
	PrivateIPAddress string `json:"private_ip_address"`
	PortID           string `json:"port_id"`
	Type             string `json:"type"`
	Status           string `json:"status"`
}

type huaweiAddress struct {
	Version string `json:"version"`
	Addr    string `json:"addr"`
	Type    string `json:"OS-EXT-IPS:type"`
	MAC     string `json:"OS-EXT-IPS-MAC:mac_addr"`
}

type huaweiServer struct {
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Addresses map[string][]huaweiAddress `json:"addresses"`
}

func collectHuawei(ctx context.Context, hc *http.Client, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	res := &model.CloudResult{Provider: target.Provider, CollectedAt: time.Now().UTC()}
	vpcEndpoint := huaweiEndpoint(target, "vpc")
	ecsEndpoint := huaweiEndpoint(target, "ecs")

	if target.CollectPrefixes {
		vpcs, err := huaweiDescribeVPCs(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询华为云 VPC 失败: %w", err)
		}
		res.VPCs = len(vpcs)
		for _, vpc := range vpcs {
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: vpc.Cidr, Name: model.First(vpc.Name, vpc.ID), VPC: vpc.ID, Region: target.Region, Kind: model.CloudPrefixVPC, Provider: target.Provider})
		}
		logf("华为云 VPC: %d 个", len(vpcs))

		subnets, err := huaweiDescribeSubnets(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询华为云子网失败: %w", err)
		}
		res.Subnets = len(subnets)
		for _, subnet := range subnets {
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: subnet.Cidr, Name: model.First(subnet.Name, subnet.ID), VPC: subnet.VpcID, Region: target.Region, Kind: model.CloudPrefixSubnet, Provider: target.Provider})
		}
		logf("华为云子网: %d 个", len(subnets))
	}

	if target.CollectInstances {
		servers, err := huaweiDescribeServers(ctx, hc, ecsEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询华为云 ECS 实例失败: %w", err)
		}
		res.Instances = len(servers)
		for _, server := range servers {
			name := model.First(server.Name, server.ID)
			for network, addresses := range server.Addresses {
				for _, address := range addresses {
					if address.Addr == "" {
						continue
					}
					public := strings.EqualFold(address.Type, "floating")
					if public && !target.CollectPublicIPs {
						continue
					}
					kind := model.CloudAddressVM
					if public {
						kind = model.CloudAddressPublic
					}
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: address.Addr, Name: name, InstanceID: server.ID, Interface: model.First(network, address.Type), MAC: address.MAC, Public: public, Kind: kind, Region: target.Region})
				}
			}
		}
		logf("华为云 ECS 实例: %d 个", len(servers))
	}

	if target.CollectPublicIPs {
		eips, err := huaweiDescribePublicIPs(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询华为云弹性公网 IP 失败: %w", err)
		}
		for _, eip := range eips {
			if eip.PublicIPAddress == "" {
				continue
			}
			res.Addresses = append(res.Addresses, model.CloudAddress{Address: eip.PublicIPAddress, Name: model.First(eip.PortID, eip.ID), Interface: eip.PortID, Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
		}
		logf("华为云弹性公网 IP: %d 个", len(eips))
	}
	return res, nil
}

func huaweiEndpoint(target model.CloudTarget, service string) string {
	if cleanBase(target.Endpoint) != "" {
		base := cleanBase(target.Endpoint)
		if strings.Contains(base, "{service}") {
			return strings.ReplaceAll(base, "{service}", service)
		}
		return base
	}
	return "https://" + service + "." + target.Region + ".myhuaweicloud.com"
}

func huaweiCall(ctx context.Context, hc *http.Client, endpoint, path string, params url.Values, target model.CloudTarget, out any) error {
	query := encodeQuery(params)
	u := cleanBase(endpoint) + path
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	signHuawei(req, nil, target.AccessKey, target.SecretKey, time.Now())
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析华为云响应失败: %w", err)
	}
	return nil
}

func huaweiProjectPath(target model.CloudTarget, suffix string) string {
	return "/v1/" + url.PathEscape(target.Project) + suffix
}

func huaweiDescribeVPCs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]huaweiVPC, error) {
	var out []huaweiVPC
	marker := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Vpcs []huaweiVPC `json:"vpcs"`
		}
		params := url.Values{}
		params.Set("limit", strconv.Itoa(pageSize))
		if marker != "" {
			params.Set("marker", marker)
		}
		if err := huaweiCall(ctx, hc, endpoint, huaweiProjectPath(target, "/vpcs"), params, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Vpcs...)
		if len(resp.Vpcs) < pageSize {
			break
		}
		marker = resp.Vpcs[len(resp.Vpcs)-1].ID
	}
	return out, nil
}

func huaweiDescribeSubnets(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]huaweiSubnet, error) {
	var out []huaweiSubnet
	marker := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Subnets []huaweiSubnet `json:"subnets"`
		}
		params := url.Values{}
		params.Set("limit", strconv.Itoa(pageSize))
		if marker != "" {
			params.Set("marker", marker)
		}
		if err := huaweiCall(ctx, hc, endpoint, huaweiProjectPath(target, "/subnets"), params, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Subnets...)
		if len(resp.Subnets) < pageSize {
			break
		}
		marker = resp.Subnets[len(resp.Subnets)-1].ID
	}
	return out, nil
}

func huaweiDescribePublicIPs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]huaweiPublicIP, error) {
	var out []huaweiPublicIP
	marker := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			PublicIPs []huaweiPublicIP `json:"publicips"`
		}
		params := url.Values{}
		params.Set("limit", strconv.Itoa(pageSize))
		if marker != "" {
			params.Set("marker", marker)
		}
		if err := huaweiCall(ctx, hc, endpoint, huaweiProjectPath(target, "/publicips"), params, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.PublicIPs...)
		if len(resp.PublicIPs) < pageSize {
			break
		}
		marker = resp.PublicIPs[len(resp.PublicIPs)-1].ID
	}
	return out, nil
}

func huaweiDescribeServers(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]huaweiServer, error) {
	var out []huaweiServer
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Servers []huaweiServer `json:"servers"`
		}
		params := url.Values{}
		params.Set("limit", strconv.Itoa(pageSize))
		params.Set("offset", strconv.Itoa(page*pageSize))
		if err := huaweiCall(ctx, hc, endpoint, huaweiProjectPath(target, "/cloudservers/detail"), params, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Servers...)
		if len(resp.Servers) < pageSize {
			break
		}
	}
	return out, nil
}
