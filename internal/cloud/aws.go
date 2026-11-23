package cloud

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

// AWS EC2 Query API version used by all calls.
const awsAPIVersion = "2016-11-15"

type awsTag struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

func awsTagValue(tags []awsTag, key string) string {
	for _, tag := range tags {
		if strings.EqualFold(tag.Key, key) {
			return tag.Value
		}
	}
	return ""
}

type awsVPC struct {
	VpcID            string   `xml:"vpcId"`
	CidrBlock        string   `xml:"cidrBlock"`
	IsDefault        bool     `xml:"isDefault"`
	Tags             []awsTag `xml:"tagSet>item"`
	CidrAssociations []struct {
		CidrBlock string `xml:"cidrBlock"`
	} `xml:"cidrBlockAssociationSet>item"`
}

type awsSubnet struct {
	SubnetID         string   `xml:"subnetId"`
	VpcID            string   `xml:"vpcId"`
	CidrBlock        string   `xml:"cidrBlock"`
	AvailabilityZone string   `xml:"availabilityZone"`
	Tags             []awsTag `xml:"tagSet>item"`
	IPv6Associations []struct {
		IPv6CidrBlock string `xml:"ipv6CidrBlock"`
	} `xml:"ipv6CidrBlockAssociationSet>item"`
}

type awsInstance struct {
	InstanceID        string   `xml:"instanceId"`
	PrivateIPAddress  string   `xml:"privateIpAddress"`
	PublicIPAddress   string   `xml:"ipAddress"`
	Tags              []awsTag `xml:"tagSet>item"`
	NetworkInterfaces []struct {
		NetworkInterfaceID string `xml:"networkInterfaceId"`
		VpcID              string `xml:"vpcId"`
		PrivateIPAddress   string `xml:"privateIpAddress"`
		MACAddress         string `xml:"macAddress"`
		PrivateIPs         []struct {
			PrivateIPAddress string `xml:"privateIpAddress"`
			Primary          bool   `xml:"primary"`
			Association      struct {
				PublicIP string `xml:"publicIp"`
			} `xml:"association"`
		} `xml:"privateIpAddressesSet>item"`
		IPv6Addresses []struct {
			IPv6Address string `xml:"ipv6Address"`
		} `xml:"ipv6AddressesSet>item"`
	} `xml:"networkInterfaceSet>item"`
}

type awsAddress struct {
	PublicIP         string   `xml:"publicIp"`
	AllocationID     string   `xml:"allocationId"`
	PrivateIPAddress string   `xml:"privateIpAddress"`
	InstanceID       string   `xml:"instanceId"`
	NetworkInterface string   `xml:"networkInterfaceId"`
	Tags             []awsTag `xml:"tags>item"`
}

func collectAWS(ctx context.Context, hc *http.Client, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	endpoint := endpointOrDefault(target.Endpoint, "https://ec2."+target.Region+".amazonaws.com")
	res := &model.CloudResult{Provider: target.Provider, CollectedAt: time.Now().UTC()}

	if target.CollectPrefixes {
		vpcs, err := awsDescribeVPCs(ctx, hc, endpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询 AWS VPC 失败: %w", err)
		}
		res.VPCs = len(vpcs)
		for _, vpc := range vpcs {
			name := model.First(awsTagValue(vpc.Tags, "Name"), vpc.VpcID, "VPC")
			cidrs := []string{vpc.CidrBlock}
			for _, assoc := range vpc.CidrAssociations {
				if assoc.CidrBlock != "" {
					cidrs = append(cidrs, assoc.CidrBlock)
				}
			}
			for _, cidr := range cidrs {
				res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: cidr, Name: name, VPC: vpc.VpcID, Region: target.Region, Kind: model.CloudPrefixVPC, Provider: target.Provider})
			}
		}
		logf("AWS VPC: %d 个", len(vpcs))

		subnets, err := awsDescribeSubnets(ctx, hc, endpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询 AWS 子网失败: %w", err)
		}
		res.Subnets = len(subnets)
		for _, subnet := range subnets {
			name := model.First(awsTagValue(subnet.Tags, "Name"), subnet.SubnetID, "Subnet")
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: subnet.CidrBlock, Name: name, VPC: subnet.VpcID, Region: target.Region, Kind: model.CloudPrefixSubnet, Provider: target.Provider})
			for _, assoc := range subnet.IPv6Associations {
				if assoc.IPv6CidrBlock != "" {
					res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: assoc.IPv6CidrBlock, Name: name, VPC: subnet.VpcID, Region: target.Region, Kind: model.CloudPrefixSubnet, Provider: target.Provider})
				}
			}
		}
		logf("AWS 子网: %d 个", len(subnets))
	}

	if target.CollectInstances {
		instances, err := awsDescribeInstances(ctx, hc, endpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询 AWS 实例失败: %w", err)
		}
		res.Instances = len(instances)
		for _, instance := range instances {
			name := model.First(awsTagValue(instance.Tags, "Name"), instance.InstanceID)
			added := false
			if instance.PrivateIPAddress != "" {
				res.Addresses = append(res.Addresses, model.CloudAddress{Address: instance.PrivateIPAddress, Name: name, InstanceID: instance.InstanceID, Kind: model.CloudAddressVM, Region: target.Region})
				added = true
			}
			for _, nic := range instance.NetworkInterfaces {
				if nic.PrivateIPAddress != "" && nic.PrivateIPAddress != instance.PrivateIPAddress {
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: nic.PrivateIPAddress, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, MAC: nic.MACAddress, Kind: model.CloudAddressVM, Region: target.Region, VPC: nic.VpcID})
					added = true
				}
				for _, ip := range nic.PrivateIPs {
					if ip.PrivateIPAddress == "" {
						continue
					}
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip.PrivateIPAddress, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, MAC: nic.MACAddress, Kind: model.CloudAddressVM, Region: target.Region, VPC: nic.VpcID})
					added = true
				}
				for _, ip := range nic.IPv6Addresses {
					if ip.IPv6Address == "" {
						continue
					}
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip.IPv6Address, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, MAC: nic.MACAddress, Kind: model.CloudAddressVM, Region: target.Region, VPC: nic.VpcID})
					added = true
				}
			}
			if target.CollectPublicIPs && instance.PublicIPAddress != "" {
				res.Addresses = append(res.Addresses, model.CloudAddress{Address: instance.PublicIPAddress, Name: name, InstanceID: instance.InstanceID, Interface: "public", Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
			}
			for _, nic := range instance.NetworkInterfaces {
				if !target.CollectPublicIPs {
					break
				}
				for _, ip := range nic.PrivateIPs {
					if ip.Association.PublicIP != "" {
						res.Addresses = append(res.Addresses, model.CloudAddress{Address: ip.Association.PublicIP, Name: name, InstanceID: instance.InstanceID, Interface: nic.NetworkInterfaceID, Public: true, Kind: model.CloudAddressPublic, Region: target.Region, VPC: nic.VpcID})
					}
				}
			}
			if !added && !target.CollectPublicIPs {
				res.AddWarning("AWS 实例 %s 没有可用的私有 IP", instance.InstanceID)
			}
		}
		logf("AWS 实例: %d 个", len(instances))
	}

	if target.CollectPublicIPs {
		addresses, err := awsDescribeAddresses(ctx, hc, endpoint, target)
		if err != nil {
			return nil, fmt.Errorf("查询 AWS 弹性 IP 失败: %w", err)
		}
		for _, address := range addresses {
			if address.PublicIP == "" {
				continue
			}
			name := model.First(awsTagValue(address.Tags, "Name"), address.InstanceID, address.AllocationID)
			res.Addresses = append(res.Addresses, model.CloudAddress{Address: address.PublicIP, Name: name, InstanceID: address.InstanceID, Interface: address.NetworkInterface, Public: true, Kind: model.CloudAddressPublic, Region: target.Region})
		}
		logf("AWS 弹性 IP: %d 个", len(addresses))
	}
	return res, nil
}

func awsCall(ctx context.Context, hc *http.Client, endpoint, region, accessKey, secretKey, action string, params url.Values, out any) error {
	q := url.Values{}
	for key, values := range params {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	q.Set("Action", action)
	q.Set("Version", awsAPIVersion)
	body := []byte(q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cleanBase(endpoint)+"/", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	signAWS(req, body, accessKey, secretKey, region, "ec2", time.Now())
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析 AWS 响应失败: %w", err)
	}
	return nil
}

func awsDescribeVPCs(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]awsVPC, error) {
	var out []awsVPC
	next := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Vpcs      []awsVPC `xml:"vpcSet>item"`
			NextToken string   `xml:"nextToken"`
		}
		params := url.Values{}
		if next != "" {
			params.Set("NextToken", next)
		}
		if err := awsCall(ctx, hc, endpoint, target.Region, target.AccessKey, target.SecretKey, "DescribeVpcs", params, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Vpcs...)
		if resp.NextToken == "" || resp.NextToken == next {
			break
		}
		next = resp.NextToken
	}
	return out, nil
}

func awsDescribeSubnets(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]awsSubnet, error) {
	var out []awsSubnet
	next := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Subnets   []awsSubnet `xml:"subnetSet>item"`
			NextToken string      `xml:"nextToken"`
		}
		params := url.Values{}
		if next != "" {
			params.Set("NextToken", next)
		}
		if err := awsCall(ctx, hc, endpoint, target.Region, target.AccessKey, target.SecretKey, "DescribeSubnets", params, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Subnets...)
		if resp.NextToken == "" || resp.NextToken == next {
			break
		}
		next = resp.NextToken
	}
	return out, nil
}

func awsDescribeInstances(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]awsInstance, error) {
	var out []awsInstance
	next := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Reservations []struct {
				Instances []awsInstance `xml:"instancesSet>item"`
			} `xml:"reservationSet>item"`
			NextToken string `xml:"nextToken"`
		}
		params := url.Values{}
		if next != "" {
			params.Set("NextToken", next)
		}
		if err := awsCall(ctx, hc, endpoint, target.Region, target.AccessKey, target.SecretKey, "DescribeInstances", params, &resp); err != nil {
			return nil, err
		}
		for _, reservation := range resp.Reservations {
			out = append(out, reservation.Instances...)
		}
		if resp.NextToken == "" || resp.NextToken == next {
			break
		}
		next = resp.NextToken
	}
	return out, nil
}

func awsDescribeAddresses(ctx context.Context, hc *http.Client, endpoint string, target model.CloudTarget) ([]awsAddress, error) {
	var resp struct {
		Addresses []awsAddress `xml:"addressesSet>item"`
	}
	if err := awsCall(ctx, hc, endpoint, target.Region, target.AccessKey, target.SecretKey, "DescribeAddresses", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Addresses, nil
}
