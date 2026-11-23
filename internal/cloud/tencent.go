package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

const tencentAPIVersion = "2017-03-12"

type tencentVPC struct {
	VpcID     string `json:"VpcId"`
	VpcName   string `json:"VpcName"`
	CidrBlock string `json:"CidrBlock"`
	IsDefault bool   `json:"IsDefault"`
}

type tencentSubnet struct {
	SubnetID   string `json:"SubnetId"`
	SubnetName string `json:"SubnetName"`
	VpcID      string `json:"VpcId"`
	CidrBlock  string `json:"CidrBlock"`
	Zone       string `json:"Zone"`
}

type tencentInstance struct {
	InstanceID         string   `json:"InstanceId"`
	InstanceName       string   `json:"InstanceName"`
	PrivateIPAddresses []string `json:"PrivateIpAddresses"`
	PublicIPAddresses  []string `json:"PublicIpAddresses"`
	NetworkInterfaces  []struct {
		NetworkInterfaceID string   `json:"NetworkInterfaceId"`
		MACAddress         string   `json:"MacAddress"`
		PrivateIPAddresses []string `json:"PrivateIpAddresses"`
		PublicIPAddresses  []string `json:"PublicIpAddresses"`
	} `json:"NetworkInterfaces"`
}

type tencentEIP struct {
	AddressID          string `json:"AddressId"`
	AddressIP          string `json:"AddressIp"`
	AddressType        string `json:"AddressType"`
	InstanceID         string `json:"InstanceId"`
	NetworkInterfaceID string `json:"NetworkInterfaceId"`
	Status             string `json:"Status"`
}

type tencentError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

func collectTencent(ctx context.Context, hc *http.Client, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	res := &model.CloudResult{Provider: target.Provider, CollectedAt: time.Now().UTC()}
	cvmEndpoint := tencentEndpoint(target, "cvm")
	vpcEndpoint := tencentEndpoint(target, "vpc")

	if target.CollectPrefixes {
		vpcs, err := tencentDescribeVPCs(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询腾讯云 VPC 失败: %w", err)
		}
		res.VPCs = len(vpcs)
		for _, vpc := range vpcs {
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: vpc.CidrBlock, Name: model.First(vpc.VpcName, vpc.VpcID), VPC: vpc.VpcID, Region: target.Region, Kind: model.CloudPrefixVPC, Provider: target.Provider})
		}
		logf("腾讯云 VPC: %d 个", len(vpcs))

		subnets, err := tencentDescribeSubnets(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询腾讯云子网失败: %w", err)
		}
		res.Subnets = len(subnets)
		for _, subnet := range subnets {
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: subnet.CidrBlock, Name: model.First(subnet.SubnetName, subnet.SubnetID), VPC: subnet.VpcID, Region: target.Region, Kind: model.CloudPrefixSubnet, Provider: target.Provider})
		}
		logf("腾讯云子网: %d 个", len(subnets))
	}

	if target.CollectInstances {
		instances, err := tencentDescribeInstances(ctx, hc, cvmEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询腾讯云实例失败: %w", err)
		}
		res.Instances = len(instances)
		for _, instance := range instances {
			name := model.First(instance.InstanceName, instance.InstanceID)
			added := map[string]bool{}
			for _, ip := range instance.PrivateIPAddresses {
				added[ip] = true
				res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip, Name: name, InstanceID: instance.InstanceID, Kind: model.CloudAddressVM, Region: target.Region})
			}
			for _, nic := range instance.NetworkInterfaces {
				for _, ip := range nic.PrivateIPAddresses {
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, MAC: nic.MACAddress, Kind: model.CloudAddressVM, Region: target.Region})
				}
				if target.CollectPublicIPs {
					for _, ip := range nic.PublicIPAddresses {
						res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
					}
				}
			}
			if target.CollectPublicIPs {
				for _, ip := range instance.PublicIPAddresses {
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip, Name: name, InstanceID: instance.InstanceID, Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
				}
			}
			if len(added) == 0 {
				res.AddWarning("腾讯云实例 %s 没有可用的私有 IP", instance.InstanceID)
			}
		}
		logf("腾讯云实例: %d 个", len(instances))
	}

	if target.CollectPublicIPs {
		eips, err := tencentDescribeEIPs(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询腾讯云弹性 IP 失败: %w", err)
		}
		for _, eip := range eips {
			if eip.AddressIP == "" {
				continue
			}
			res.Addresses = append(res.Addresses, model.CloudAddress{Address: eip.AddressIP, Name: model.First(eip.InstanceID, eip.AddressID), InstanceID: eip.InstanceID, Interface: eip.NetworkInterfaceID, Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
		}
		logf("腾讯云弹性 IP: %d 个", len(eips))
	}
	return res, nil
}

func tencentEndpoint(target model.CloudTarget, service string) string {
	if cleanBase(target.Endpoint) != "" {
		base := cleanBase(target.Endpoint)
		if strings.Contains(base, "{service}") {
			return strings.ReplaceAll(base, "{service}", service)
		}
		return base
	}
	return "https://" + service + ".tencentcloudapi.com"
}

func tencentCall(ctx context.Context, hc *http.Client, endpoint, action, region string, payload map[string]any, target model.CloudTarget, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cleanBase(endpoint)+"/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-TC-Version", tencentAPIVersion)
	req.Header.Set("Accept", "application/json")
	signTencent(req, body, target.AccessKey, target.SecretKey, region, tencentService(action), action, time.Now())
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return err
	}
	var envelope struct {
		Response json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("解析腾讯云响应失败: %w", err)
	}
	if len(envelope.Response) == 0 {
		return fmt.Errorf("腾讯云响应缺少 Response 字段")
	}
	var meta struct {
		Error     *tencentError `json:"Error"`
		RequestID string        `json:"RequestId"`
	}
	if err := json.Unmarshal(envelope.Response, &meta); err != nil {
		return fmt.Errorf("解析腾讯云响应失败: %w", err)
	}
	if meta.Error != nil {
		return fmt.Errorf("腾讯云 API 错误 %s: %s", meta.Error.Code, meta.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Response, out); err != nil {
			return fmt.Errorf("解析腾讯云响应失败: %w", err)
		}
	}
	return nil
}

// tencentService maps an action to the TC3 service name.
func tencentService(action string) string {
	switch action {
	case "DescribeVpcs", "DescribeSubnets", "DescribeAddresses":
		return "vpc"
	default:
		return "cvm"
	}
}

func tencentDescribeVPCs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]tencentVPC, error) {
	var out []tencentVPC
	for page := 0; page < maxPages; page++ {
		var resp struct {
			TotalCount int          `json:"TotalCount"`
			VpcSet     []tencentVPC `json:"VpcSet"`
		}
		payload := map[string]any{"Offset": page * pageSize, "Limit": pageSize}
		if err := tencentCall(ctx, hc, endpoint, "DescribeVpcs", target.Region, payload, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.VpcSet...)
		if len(resp.VpcSet) == 0 || len(out) >= resp.TotalCount {
			break
		}
	}
	return out, nil
}

func tencentDescribeSubnets(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]tencentSubnet, error) {
	var out []tencentSubnet
	for page := 0; page < maxPages; page++ {
		var resp struct {
			TotalCount int             `json:"TotalCount"`
			SubnetSet  []tencentSubnet `json:"SubnetSet"`
		}
		payload := map[string]any{"Offset": page * pageSize, "Limit": pageSize}
		if err := tencentCall(ctx, hc, endpoint, "DescribeSubnets", target.Region, payload, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.SubnetSet...)
		if len(resp.SubnetSet) == 0 || len(out) >= resp.TotalCount {
			break
		}
	}
	return out, nil
}

func tencentDescribeInstances(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]tencentInstance, error) {
	var out []tencentInstance
	for page := 0; page < maxPages; page++ {
		var resp struct {
			TotalCount  int               `json:"TotalCount"`
			InstanceSet []tencentInstance `json:"InstanceSet"`
		}
		payload := map[string]any{"Offset": page * pageSize, "Limit": pageSize}
		if err := tencentCall(ctx, hc, endpoint, "DescribeInstances", target.Region, payload, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.InstanceSet...)
		if len(resp.InstanceSet) == 0 || len(out) >= resp.TotalCount {
			break
		}
	}
	return out, nil
}

func tencentDescribeEIPs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]tencentEIP, error) {
	var out []tencentEIP
	for page := 0; page < maxPages; page++ {
		var resp struct {
			TotalCount int          `json:"TotalCount"`
			AddressSet []tencentEIP `json:"AddressSet"`
		}
		payload := map[string]any{"Offset": page * pageSize, "Limit": pageSize}
		if err := tencentCall(ctx, hc, endpoint, "DescribeAddresses", target.Region, payload, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.AddressSet...)
		if len(resp.AddressSet) == 0 || len(out) >= resp.TotalCount {
			break
		}
	}
	return out, nil
}
