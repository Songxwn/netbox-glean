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

const (
	aliyunECSVersion = "2014-05-26"
	aliyunVPCVersion = "2016-04-28"
)

type aliyunVPC struct {
	VpcID     string `json:"VpcId"`
	VpcName   string `json:"VpcName"`
	CidrBlock string `json:"CidrBlock"`
	RegionID  string `json:"RegionId"`
	IsDefault bool   `json:"IsDefault"`
}

type aliyunSwitch struct {
	VSwitchID   string `json:"VSwitchId"`
	VSwitchName string `json:"VSwitchName"`
	VpcID       string `json:"VpcId"`
	ZoneID      string `json:"ZoneId"`
	CidrBlock   string `json:"CidrBlock"`
}

type aliyunIPSet struct {
	IPAddress []string `json:"IpAddress"`
}

type aliyunInstance struct {
	InstanceID    string `json:"InstanceId"`
	InstanceName  string `json:"InstanceName"`
	RegionID      string `json:"RegionId"`
	VpcAttributes struct {
		VpcID            string      `json:"VpcId"`
		VSwitchID        string      `json:"VSwitchId"`
		PrivateIPAddress aliyunIPSet `json:"PrivateIpAddress"`
	} `json:"VpcAttributes"`
	PublicIPAddress aliyunIPSet `json:"PublicIpAddress"`
	EipAddress      struct {
		AllocationID string `json:"AllocationId"`
		IPAddress    string `json:"IpAddress"`
	} `json:"EipAddress"`
	NetworkInterfaces struct {
		NetworkInterface []struct {
			NetworkInterfaceID string      `json:"NetworkInterfaceId"`
			MACAddress         string      `json:"MacAddress"`
			PrimaryIPAddress   string      `json:"PrimaryIpAddress"`
			PrivateIPAddress   aliyunIPSet `json:"PrivateIpAddress"`
			PublicIPAddress    aliyunIPSet `json:"PublicIpAddress"`
			AssociatedPublicIP struct {
				PublicIPAddress string `json:"PublicIpAddress"`
			} `json:"AssociatedPublicIp"`
		} `json:"NetworkInterface"`
	} `json:"NetworkInterfaces"`
}

type aliyunEIP struct {
	AllocationID string `json:"AllocationId"`
	IPAddress    string `json:"IpAddress"`
	InstanceID   string `json:"InstanceId"`
	InstanceType string `json:"InstanceType"`
	RegionID     string `json:"RegionId"`
	Status       string `json:"Status"`
}

func collectAliyun(ctx context.Context, hc *http.Client, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	res := &model.CloudResult{Provider: target.Provider, CollectedAt: time.Now().UTC()}
	ecsEndpoint := aliyunEndpoint(target, "ecs")
	vpcEndpoint := aliyunEndpoint(target, "vpc")

	if target.CollectPrefixes {
		vpcs, err := aliyunDescribeVPCs(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询阿里云 VPC 失败: %w", err)
		}
		res.VPCs = len(vpcs)
		for _, vpc := range vpcs {
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: vpc.CidrBlock, Name: model.First(vpc.VpcName, vpc.VpcID), VPC: vpc.VpcID, Region: model.First(vpc.RegionID, target.Region), Kind: model.CloudPrefixVPC, Provider: target.Provider})
		}
		logf("阿里云 VPC: %d 个", len(vpcs))

		switches, err := aliyunDescribeSwitches(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询阿里云交换机失败: %w", err)
		}
		res.Subnets = len(switches)
		for _, sw := range switches {
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: sw.CidrBlock, Name: model.First(sw.VSwitchName, sw.VSwitchID), VPC: sw.VpcID, Region: target.Region, Kind: model.CloudPrefixSwitch, Provider: target.Provider})
		}
		logf("阿里云交换机: %d 个", len(switches))
	}

	if target.CollectInstances {
		instances, err := aliyunDescribeInstances(ctx, hc, ecsEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询阿里云 ECS 实例失败: %w", err)
		}
		res.Instances = len(instances)
		for _, instance := range instances {
			name := model.First(instance.InstanceName, instance.InstanceID)
			for _, ip := range instance.VpcAttributes.PrivateIPAddress.IPAddress {
				res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip, Name: name, InstanceID: instance.InstanceID, Kind: model.CloudAddressVM, Region: model.First(instance.RegionID, target.Region), VPC: instance.VpcAttributes.VpcID})
			}
			for _, nic := range instance.NetworkInterfaces.NetworkInterface {
				privateIPs := nic.PrivateIPAddress.IPAddress
				if len(privateIPs) == 0 && nic.PrimaryIPAddress != "" {
					privateIPs = []string{nic.PrimaryIPAddress}
				}
				for _, ip := range privateIPs {
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, MAC: nic.MACAddress, Kind: model.CloudAddressVM, Region: model.First(instance.RegionID, target.Region), VPC: instance.VpcAttributes.VpcID})
				}
			}
			if target.CollectPublicIPs {
				public := append([]string(nil), instance.PublicIPAddress.IPAddress...)
				if instance.EipAddress.IPAddress != "" {
					public = append(public, instance.EipAddress.IPAddress)
				}
				for _, nic := range instance.NetworkInterfaces.NetworkInterface {
					public = append(public, nic.PublicIPAddress.IPAddress...)
					if nic.AssociatedPublicIP.PublicIPAddress != "" {
						public = append(public, nic.AssociatedPublicIP.PublicIPAddress)
					}
				}
				for _, ip := range public {
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip, Name: name, InstanceID: instance.InstanceID, Public: true, Kind: model.CloudAddressPublic, Region: model.First(instance.RegionID, target.Region), VPC: instance.VpcAttributes.VpcID})
				}
			}
		}
		logf("阿里云 ECS 实例: %d 个", len(instances))
	}

	if target.CollectPublicIPs {
		eips, err := aliyunDescribeEIPs(ctx, hc, vpcEndpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询阿里云弹性公网 IP 失败: %w", err)
		}
		for _, eip := range eips {
			if eip.IPAddress == "" {
				continue
			}
			res.Addresses = append(res.Addresses, model.CloudAddress{Address: eip.IPAddress, Name: model.First(eip.InstanceID, eip.AllocationID), InstanceID: eip.InstanceID, Public: true, Kind: model.CloudAddressPublic, Region: model.First(eip.RegionID, target.Region)})
		}
		logf("阿里云弹性公网 IP: %d 个", len(eips))
	}
	return res, nil
}

// aliyunEndpoint resolves the ECS or VPC endpoint. A custom Endpoint may
// contain the {service} placeholder.
func aliyunEndpoint(target model.CloudTarget, service string) string {
	if cleanBase(target.Endpoint) != "" {
		base := cleanBase(target.Endpoint)
		if strings.Contains(base, "{service}") {
			return strings.ReplaceAll(base, "{service}", service)
		}
		return base
	}
	return "https://" + service + "." + target.Region + ".aliyuncs.com"
}

func aliyunCall(ctx context.Context, hc *http.Client, endpoint, version, action string, params url.Values, target model.CloudTarget, out any) error {
	u := aliyunURL(endpoint, version, action, params, target.AccessKey, target.SecretKey, time.Now())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析阿里云响应失败: %w", err)
	}
	return nil
}

func aliyunDescribeVPCs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]aliyunVPC, error) {
	var out []aliyunVPC
	for page := 1; page <= maxPages; page++ {
		var raw struct {
			TotalCount int `json:"TotalCount"`
			Vpcs       struct {
				VPC []aliyunVPC `json:"Vpc"`
			} `json:"Vpcs"`
		}
		params := url.Values{}
		params.Set("RegionId", target.Region)
		params.Set("PageNumber", strconv.Itoa(page))
		params.Set("PageSize", strconv.Itoa(pageSize))
		if err := aliyunCall(ctx, hc, endpoint, aliyunVPCVersion, "DescribeVpcs", params, target, &raw); err != nil {
			return nil, err
		}
		out = append(out, raw.Vpcs.VPC...)
		if len(raw.Vpcs.VPC) == 0 || len(out) >= raw.TotalCount {
			break
		}
	}
	return out, nil
}

func aliyunDescribeSwitches(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]aliyunSwitch, error) {
	var out []aliyunSwitch
	for page := 1; page <= maxPages; page++ {
		var raw struct {
			TotalCount int `json:"TotalCount"`
			VSwitches  struct {
				VSwitch []aliyunSwitch `json:"VSwitch"`
			} `json:"VSwitches"`
		}
		params := url.Values{}
		params.Set("RegionId", target.Region)
		params.Set("PageNumber", strconv.Itoa(page))
		params.Set("PageSize", strconv.Itoa(pageSize))
		if err := aliyunCall(ctx, hc, endpoint, aliyunVPCVersion, "DescribeVSwitches", params, target, &raw); err != nil {
			return nil, err
		}
		out = append(out, raw.VSwitches.VSwitch...)
		if len(raw.VSwitches.VSwitch) == 0 || len(out) >= raw.TotalCount {
			break
		}
	}
	return out, nil
}

func aliyunDescribeInstances(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]aliyunInstance, error) {
	var out []aliyunInstance
	for page := 1; page <= maxPages; page++ {
		var raw struct {
			TotalCount int `json:"TotalCount"`
			Instances  struct {
				Instance []aliyunInstance `json:"Instance"`
			} `json:"Instances"`
		}
		params := url.Values{}
		params.Set("RegionId", target.Region)
		params.Set("PageNumber", strconv.Itoa(page))
		params.Set("PageSize", strconv.Itoa(pageSize))
		if err := aliyunCall(ctx, hc, endpoint, aliyunECSVersion, "DescribeInstances", params, target, &raw); err != nil {
			return nil, err
		}
		out = append(out, raw.Instances.Instance...)
		if len(raw.Instances.Instance) == 0 || len(out) >= raw.TotalCount {
			break
		}
	}
	return out, nil
}

func aliyunDescribeEIPs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]aliyunEIP, error) {
	var out []aliyunEIP
	for page := 1; page <= maxPages; page++ {
		var raw struct {
			TotalCount   int `json:"TotalCount"`
			EipAddresses struct {
				EIP []aliyunEIP `json:"EipAddress"`
			} `json:"EipAddresses"`
		}
		params := url.Values{}
		params.Set("RegionId", target.Region)
		params.Set("PageNumber", strconv.Itoa(page))
		params.Set("PageSize", strconv.Itoa(pageSize))
		if err := aliyunCall(ctx, hc, endpoint, aliyunVPCVersion, "DescribeEipAddresses", params, target, &raw); err != nil {
			return nil, err
		}
		out = append(out, raw.EipAddresses.EIP...)
		if len(raw.EipAddresses.EIP) == 0 || len(out) >= raw.TotalCount {
			break
		}
	}
	return out, nil
}
