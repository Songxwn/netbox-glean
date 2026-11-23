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

const volcengineAPIVersion = "2020-04-01"

type volcEIPAddress struct {
	IPAddress    string
	AllocationID string
}

func (e *volcEIPAddress) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		e.IPAddress = value
		return nil
	}
	var obj struct {
		IPAddress    string `json:"IpAddress"`
		AllocationID string `json:"AllocationId"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	e.IPAddress = obj.IPAddress
	e.AllocationID = obj.AllocationID
	return nil
}

type volcIPList []string

func (l *volcIPList) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		text := strings.TrimSpace(string(raw))
		if text == "" || text == "null" {
			continue
		}
		if text[0] == '"' {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			out = append(out, value)
			continue
		}
		var obj struct {
			IPAddress string `json:"IpAddress"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		if obj.IPAddress != "" {
			out = append(out, obj.IPAddress)
		}
	}
	*l = out
	return nil
}

type volcVPC struct {
	VpcID     string `json:"VpcId"`
	VpcName   string `json:"VpcName"`
	CidrBlock string `json:"CidrBlock"`
	RegionID  string `json:"RegionId"`
	Status    string `json:"Status"`
}

type volcSubnet struct {
	SubnetID   string `json:"SubnetId"`
	SubnetName string `json:"SubnetName"`
	CidrBlock  string `json:"CidrBlock"`
	VpcID      string `json:"VpcId"`
	ZoneID     string `json:"ZoneId"`
	Status     string `json:"Status"`
}

type volcInstance struct {
	InstanceID        string         `json:"InstanceId"`
	InstanceName      string         `json:"InstanceName"`
	ZoneID            string         `json:"ZoneId"`
	EIPAddress        volcEIPAddress `json:"EipAddress"`
	NetworkInterfaces []struct {
		NetworkInterfaceID string         `json:"NetworkInterfaceId"`
		MACAddress         string         `json:"MacAddress"`
		PrimaryIPAddress   string         `json:"PrimaryIpAddress"`
		PrivateIPAddresses volcIPList     `json:"PrivateIpAddressSet"`
		EIPAddress         volcEIPAddress `json:"EipAddress"`
	} `json:"NetworkInterfaces"`
}

type volcEIP struct {
	AllocationID string `json:"AllocationId"`
	EipAddress   string `json:"EipAddress"`
	InstanceID   string `json:"InstanceId"`
	InstanceType string `json:"InstanceType"`
	Status       string `json:"Status"`
}

func collectVolcengine(ctx context.Context, hc *http.Client, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	res := &model.CloudResult{Provider: target.Provider, CollectedAt: time.Now().UTC()}
	endpoint := endpointOrDefault(target.Endpoint, "https://open.volcengineapi.com")

	if target.CollectPrefixes {
		vpcs, err := volcDescribeVPCs(ctx, hc, endpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询火山引擎 VPC 失败: %w", err)
		}
		res.VPCs = len(vpcs)
		for _, vpc := range vpcs {
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: vpc.CidrBlock, Name: model.First(vpc.VpcName, vpc.VpcID), VPC: vpc.VpcID, Region: model.First(vpc.RegionID, target.Region), Kind: model.CloudPrefixVPC, Provider: target.Provider})
		}
		logf("火山引擎 VPC: %d 个", len(vpcs))

		subnets, err := volcDescribeSubnets(ctx, hc, endpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询火山引擎子网失败: %w", err)
		}
		res.Subnets = len(subnets)
		for _, subnet := range subnets {
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: subnet.CidrBlock, Name: model.First(subnet.SubnetName, subnet.SubnetID), VPC: subnet.VpcID, Region: target.Region, Kind: model.CloudPrefixSubnet, Provider: target.Provider})
		}
		logf("火山引擎子网: %d 个", len(subnets))
	}

	if target.CollectInstances {
		instances, err := volcDescribeInstances(ctx, hc, endpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询火山引擎实例失败: %w", err)
		}
		res.Instances = len(instances)
		for _, instance := range instances {
			name := model.First(instance.InstanceName, instance.InstanceID)
			added := map[string]bool{}
			for _, nic := range instance.NetworkInterfaces {
				private := append([]string(nil), nic.PrivateIPAddresses...)
				if len(private) == 0 && nic.PrimaryIPAddress != "" {
					private = []string{nic.PrimaryIPAddress}
				}
				for _, ip := range private {
					added[ip] = true
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, MAC: nic.MACAddress, Kind: model.CloudAddressVM, Region: target.Region})
				}
				if target.CollectPublicIPs && nic.EIPAddress.IPAddress != "" {
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: nic.EIPAddress.IPAddress, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
				}
			}
			if target.CollectPublicIPs && instance.EIPAddress.IPAddress != "" {
				res.Addresses = append(res.Addresses, model.CloudAddress{Address: instance.EIPAddress.IPAddress, Name: name, InstanceID: instance.InstanceID, Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
			}
			if len(added) == 0 {
				res.AddWarning("火山引擎实例 %s 没有可用的私有 IP", instance.InstanceID)
			}
		}
		logf("火山引擎实例: %d 个", len(instances))
	}

	if target.CollectPublicIPs {
		eips, err := volcDescribeEIPs(ctx, hc, endpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询火山引擎弹性公网 IP 失败: %w", err)
		}
		for _, eip := range eips {
			if eip.EipAddress == "" {
				continue
			}
			res.Addresses = append(res.Addresses, model.CloudAddress{Address: eip.EipAddress, Name: model.First(eip.InstanceID, eip.AllocationID), InstanceID: eip.InstanceID, Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
		}
		logf("火山引擎弹性公网 IP: %d 个", len(eips))
	}
	return res, nil
}

func volcService(action string) string {
	switch action {
	case "DescribeVpcs", "DescribeSubnets", "DescribeEipAddresses":
		return "vpc"
	default:
		return "ecs"
	}
}

func volcCall(ctx context.Context, hc *http.Client, endpoint, action string, page int, target model.CloudTarget, out any) error {
	q := url.Values{}
	q.Set("Action", action)
	q.Set("Version", volcengineAPIVersion)
	q.Set("Region", target.Region)
	q.Set("PageNumber", strconv.Itoa(page))
	q.Set("PageSize", strconv.Itoa(pageSize))
	u := cleanBase(endpoint) + "/?" + encodeQuery(q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	signVolcengine(req, nil, target.AccessKey, target.SecretKey, target.Region, volcService(action), time.Now())
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return err
	}
	var envelope struct {
		Metadata struct {
			RequestID string `json:"RequestId"`
			Error     *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Result json.RawMessage `json:"Result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("解析火山引擎响应失败: %w", err)
	}
	if envelope.Metadata.Error != nil {
		return fmt.Errorf("火山引擎 API 错误 %s: %s", envelope.Metadata.Error.Code, envelope.Metadata.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("解析火山引擎 Result 失败: %w", err)
		}
	}
	return nil
}

func volcDescribeVPCs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]volcVPC, error) {
	var out []volcVPC
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			TotalCount int       `json:"TotalCount"`
			Vpcs       []volcVPC `json:"Vpcs"`
		}
		if err := volcCall(ctx, hc, endpoint, "DescribeVpcs", page, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Vpcs...)
		if len(resp.Vpcs) == 0 || len(out) >= resp.TotalCount {
			break
		}
	}
	return out, nil
}

func volcDescribeSubnets(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]volcSubnet, error) {
	var out []volcSubnet
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			TotalCount int          `json:"TotalCount"`
			Subnets    []volcSubnet `json:"Subnets"`
		}
		if err := volcCall(ctx, hc, endpoint, "DescribeSubnets", page, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Subnets...)
		if len(resp.Subnets) == 0 || len(out) >= resp.TotalCount {
			break
		}
	}
	return out, nil
}

func volcDescribeInstances(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]volcInstance, error) {
	var out []volcInstance
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			TotalCount int            `json:"TotalCount"`
			Instances  []volcInstance `json:"Instances"`
		}
		if err := volcCall(ctx, hc, endpoint, "DescribeInstances", page, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Instances...)
		if len(resp.Instances) == 0 || len(out) >= resp.TotalCount {
			break
		}
	}
	return out, nil
}

func volcDescribeEIPs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]volcEIP, error) {
	var out []volcEIP
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			TotalCount   int       `json:"TotalCount"`
			EipAddresses []volcEIP `json:"EipAddresses"`
		}
		if err := volcCall(ctx, hc, endpoint, "DescribeEipAddresses", page, target, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.EipAddresses...)
		if len(resp.EipAddresses) == 0 || len(out) >= resp.TotalCount {
			break
		}
	}
	return out, nil
}
