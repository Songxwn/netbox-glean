package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Songxwn/netbox-glean/internal/model"
)

func testLogger(t *testing.T) model.LogFunc {
	t.Helper()
	return func(format string, args ...any) {
		t.Logf(format, args...)
	}
}

func TestAWSEncode(t *testing.T) {
	cases := map[string]string{
		"abc":       "abc",
		"a b":       "a%20b",
		"a/b":       "a%2Fb",
		"a~b_c-d.e": "a~b_c-d.e",
		"中文":        "%E4%B8%AD%E6%96%87",
		"a+b":       "a%2Bb",
		"100%":      "100%25",
	}
	for input, want := range cases {
		if got := awsEncode(input); got != want {
			t.Errorf("awsEncode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEncodeQueryIsSorted(t *testing.T) {
	q := url.Values{}
	q.Add("b", "2")
	q.Add("b", "1")
	q.Set("a", "x y")
	got := encodeQuery(q)
	want := "a=x%20y&b=1&b=2"
	if got != want {
		t.Errorf("encodeQuery = %q, want %q", got, want)
	}
}

func TestFinalizeNormalizesAndDeduplicates(t *testing.T) {
	target := model.CloudTarget{Provider: model.CloudAliyun, CollectPrefixes: true, CollectInstances: true}
	res := &model.CloudResult{
		Prefixes: []model.CloudPrefix{
			{CIDR: "10.0.1.5/24", Name: "subnet"},
			{CIDR: "10.0.1.0/24", Name: "duplicate"},
			{CIDR: "not-a-cidr"},
		},
		Addresses: []model.CloudAddress{
			{Address: "10.0.1.10", Name: "web"},
			{Address: "10.0.1.10/32", Name: "duplicate"},
			{Address: "127.0.0.1"},
			{Address: "fe80::1"},
			{Address: "2001:db8::10"},
		},
	}
	finalize(res, target)
	if len(res.Prefixes) != 1 || res.Prefixes[0].CIDR != "10.0.1.0/24" {
		t.Fatalf("prefixes not normalized: %#v", res.Prefixes)
	}
	if len(res.Addresses) != 2 {
		t.Fatalf("addresses not filtered: %#v", res.Addresses)
	}
	if res.PublicIPs != 0 {
		t.Errorf("PublicIPs = %d, want 0", res.PublicIPs)
	}
}

func TestAWSCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/xml")
		switch r.Form.Get("Action") {
		case "DescribeVpcs":
			fmt.Fprint(w, `<DescribeVpcsResponse><vpcSet><item><vpcId>vpc-1</vpcId><cidrBlock>10.0.0.0/16</cidrBlock><tagSet><item><key>Name</key><value>prod</value></item></tagSet></item></vpcSet></DescribeVpcsResponse>`)
		case "DescribeSubnets":
			fmt.Fprint(w, `<DescribeSubnetsResponse><subnetSet><item><subnetId>subnet-1</subnetId><vpcId>vpc-1</vpcId><cidrBlock>10.0.1.0/24</cidrBlock><tagSet><item><key>Name</key><value>web</value></item></tagSet></item></subnetSet></DescribeSubnetsResponse>`)
		case "DescribeInstances":
			fmt.Fprint(w, `<DescribeInstancesResponse><reservationSet><item><instancesSet><item>
				<instanceId>i-1</instanceId>
				<privateIpAddress>10.0.1.10</privateIpAddress>
				<ipAddress>54.1.2.3</ipAddress>
				<tagSet><item><key>Name</key><value>web</value></item></tagSet>
				<networkInterfaceSet><item>
					<networkInterfaceId>eni-1</networkInterfaceId>
					<vpcId>vpc-1</vpcId>
					<privateIpAddress>10.0.1.10</privateIpAddress>
					<macAddress>02:00:00:00:00:01</macAddress>
					<privateIpAddressesSet><item>
						<privateIpAddress>10.0.1.11</privateIpAddress>
						<association><publicIp>54.1.2.4</publicIp></association>
					</item></privateIpAddressesSet>
				</item></networkInterfaceSet>
			</item></instancesSet></item></reservationSet></DescribeInstancesResponse>`)
		case "DescribeAddresses":
			fmt.Fprint(w, `<DescribeAddressesResponse><addressesSet><item>
				<publicIp>54.1.2.9</publicIp>
				<allocationId>eipalloc-1</allocationId>
				<instanceId>i-1</instanceId>
				<networkInterfaceId>eni-1</networkInterfaceId>
				<tags><item><key>Name</key><value>eip</value></item></tags>
			</item></addressesSet></DescribeAddressesResponse>`)
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	target := model.CloudTarget{
		Provider:         model.CloudAWS,
		Region:           "us-east-1",
		AccessKey:        "AKIDEXAMPLE",
		SecretKey:        "secret",
		Endpoint:         server.URL,
		CollectPrefixes:  true,
		CollectInstances: true,
		CollectPublicIPs: true,
	}
	res, err := Run(context.Background(), target, testLogger(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.VPCs != 1 || res.Subnets != 1 || res.Instances != 1 {
		t.Errorf("counts = vpc %d subnets %d instances %d", res.VPCs, res.Subnets, res.Instances)
	}
	if len(res.Prefixes) != 2 {
		t.Errorf("prefixes = %#v", res.Prefixes)
	}
	assertAddresses(t, res.Addresses, "10.0.1.10", "10.0.1.11", "54.1.2.3", "54.1.2.4", "54.1.2.9")
}

func TestAliyunCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("Action") {
		case "DescribeVpcs":
			fmt.Fprint(w, `{"TotalCount":1,"Vpcs":{"Vpc":[{"VpcId":"vpc-1","VpcName":"prod","CidrBlock":"10.0.0.0/16","RegionId":"cn-hangzhou"}]}}`)
		case "DescribeVSwitches":
			fmt.Fprint(w, `{"TotalCount":1,"VSwitches":{"VSwitch":[{"VSwitchId":"vsw-1","VSwitchName":"web","VpcId":"vpc-1","CidrBlock":"10.0.1.0/24"}]}}`)
		case "DescribeInstances":
			fmt.Fprint(w, `{"TotalCount":1,"Instances":{"Instance":[{"InstanceId":"i-1","InstanceName":"web","RegionId":"cn-hangzhou","VpcAttributes":{"VpcId":"vpc-1","PrivateIpAddress":{"IpAddress":["10.0.1.10"]}},"EipAddress":{"AllocationId":"eip-1","IpAddress":"47.1.2.3"}}]}}`)
		case "DescribeEipAddresses":
			fmt.Fprint(w, `{"TotalCount":1,"EipAddresses":{"EipAddress":[{"AllocationId":"eip-2","IpAddress":"47.1.2.4","InstanceId":"i-1","RegionId":"cn-hangzhou"}]}}`)
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	target := model.CloudTarget{
		Provider:         model.CloudAliyun,
		Region:           "cn-hangzhou",
		AccessKey:        "ak",
		SecretKey:        "sk",
		Endpoint:         server.URL,
		CollectPrefixes:  true,
		CollectInstances: true,
		CollectPublicIPs: true,
	}
	res, err := Run(context.Background(), target, testLogger(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Prefixes) != 2 {
		t.Errorf("prefixes = %#v", res.Prefixes)
	}
	assertAddresses(t, res.Addresses, "10.0.1.10", "47.1.2.3", "47.1.2.4")
}

func TestTencentCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-TC-Action") {
		case "DescribeVpcs":
			fmt.Fprint(w, `{"Response":{"TotalCount":1,"VpcSet":[{"VpcId":"vpc-1","VpcName":"prod","CidrBlock":"10.0.0.0/16"}],"RequestId":"r"}}`)
		case "DescribeSubnets":
			fmt.Fprint(w, `{"Response":{"TotalCount":1,"SubnetSet":[{"SubnetId":"subnet-1","SubnetName":"web","VpcId":"vpc-1","CidrBlock":"10.0.1.0/24"}],"RequestId":"r"}}`)
		case "DescribeInstances":
			fmt.Fprint(w, `{"Response":{"TotalCount":1,"InstanceSet":[{"InstanceId":"ins-1","InstanceName":"web","PrivateIpAddresses":["10.0.1.10"],"PublicIpAddresses":["43.1.2.3"],"NetworkInterfaces":[{"NetworkInterfaceId":"eni-1","MacAddress":"02:00:00:00:00:01","PrivateIpAddresses":["10.0.1.11"],"PublicIpAddresses":["43.1.2.4"]}]}],"RequestId":"r"}}`)
		case "DescribeAddresses":
			fmt.Fprint(w, `{"Response":{"TotalCount":1,"AddressSet":[{"AddressId":"eip-1","AddressIp":"43.1.2.5","InstanceId":"ins-1"}],"RequestId":"r"}}`)
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	target := model.CloudTarget{
		Provider:         model.CloudTencent,
		Region:           "ap-guangzhou",
		AccessKey:        "ak",
		SecretKey:        "sk",
		Endpoint:         server.URL,
		CollectPrefixes:  true,
		CollectInstances: true,
		CollectPublicIPs: true,
	}
	res, err := Run(context.Background(), target, testLogger(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Prefixes) != 2 {
		t.Errorf("prefixes = %#v", res.Prefixes)
	}
	assertAddresses(t, res.Addresses, "10.0.1.10", "10.0.1.11", "43.1.2.3", "43.1.2.4", "43.1.2.5")
}

func TestHuaweiCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/proj/vpcs":
			fmt.Fprint(w, `{"vpcs":[{"id":"vpc-1","name":"prod","cidr":"10.0.0.0/16"}]}`)
		case "/v1/proj/subnets":
			fmt.Fprint(w, `{"subnets":[{"id":"subnet-1","name":"web","cidr":"10.0.1.0/24","vpc_id":"vpc-1"}]}`)
		case "/v1/proj/cloudservers/detail":
			fmt.Fprint(w, `{"servers":[{"id":"srv-1","name":"web","addresses":{"net1":[{"version":"4","addr":"10.0.1.10","OS-EXT-IPS:type":"fixed","OS-EXT-IPS-MAC:mac_addr":"02:00:00:00:00:01"},{"version":"4","addr":"47.1.2.3","OS-EXT-IPS:type":"floating"}]}}]}`)
		case "/v1/proj/publicips":
			fmt.Fprint(w, `{"publicips":[{"id":"eip-1","public_ip_address":"47.1.2.4","port_id":"port-1"}]}`)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	target := model.CloudTarget{
		Provider:         model.CloudHuawei,
		Region:           "cn-north-4",
		Project:          "proj",
		AccessKey:        "ak",
		SecretKey:        "sk",
		Endpoint:         server.URL,
		CollectPrefixes:  true,
		CollectInstances: true,
		CollectPublicIPs: true,
	}
	res, err := Run(context.Background(), target, testLogger(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Prefixes) != 2 {
		t.Errorf("prefixes = %#v", res.Prefixes)
	}
	assertAddresses(t, res.Addresses, "10.0.1.10", "47.1.2.3", "47.1.2.4")
}

func TestVolcengineCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("Action") {
		case "DescribeVpcs":
			fmt.Fprint(w, `{"ResponseMetadata":{"RequestId":"r"},"Result":{"TotalCount":1,"Vpcs":[{"VpcId":"vpc-1","VpcName":"prod","CidrBlock":"10.0.0.0/16","RegionId":"cn-beijing"}]}}`)
		case "DescribeSubnets":
			fmt.Fprint(w, `{"ResponseMetadata":{"RequestId":"r"},"Result":{"TotalCount":1,"Subnets":[{"SubnetId":"subnet-1","SubnetName":"web","CidrBlock":"10.0.1.0/24","VpcId":"vpc-1"}]}}`)
		case "DescribeInstances":
			fmt.Fprint(w, `{"ResponseMetadata":{"RequestId":"r"},"Result":{"TotalCount":1,"Instances":[{"InstanceId":"i-1","InstanceName":"web","EipAddress":{"IpAddress":"47.1.2.3","AllocationId":"eip-1"},"NetworkInterfaces":[{"NetworkInterfaceId":"eni-1","MacAddress":"02:00:00:00:00:01","PrimaryIpAddress":"10.0.1.10","PrivateIpAddressSet":["10.0.1.10"],"EipAddress":{"IpAddress":"47.1.2.4","AllocationId":"eip-1"}}]}]}}`)
		case "DescribeEipAddresses":
			fmt.Fprint(w, `{"ResponseMetadata":{"RequestId":"r"},"Result":{"TotalCount":1,"EipAddresses":[{"AllocationId":"eip-2","EipAddress":"47.1.2.5","InstanceId":"i-1"}]}}`)
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	target := model.CloudTarget{
		Provider:         model.CloudVolcengine,
		Region:           "cn-beijing",
		AccessKey:        "ak",
		SecretKey:        "sk",
		Endpoint:         server.URL,
		CollectPrefixes:  true,
		CollectInstances: true,
		CollectPublicIPs: true,
	}
	res, err := Run(context.Background(), target, testLogger(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Prefixes) != 2 {
		t.Errorf("prefixes = %#v", res.Prefixes)
	}
	assertAddresses(t, res.Addresses, "10.0.1.10", "47.1.2.3", "47.1.2.4", "47.1.2.5")
}

func TestGCPListHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/aggregated/subnetworks"):
			fmt.Fprint(w, `{"items":{"regions/us-central1":{"subnetworks":[{"name":"default","network":"projects/p/global/networks/default","ipCidrRange":"10.0.0.0/24","region":"regions/us-central1"}]}}}`)
		case strings.HasSuffix(r.URL.Path, "/aggregated/instances"):
			fmt.Fprint(w, `{"items":{"zones/us-central1-a":{"instances":[{"name":"web","id":1,"networkInterfaces":[{"network":"projects/p/global/networks/default","subnetwork":"projects/p/regions/us-central1/subnetworks/default","networkIP":"10.0.0.10","accessConfigs":[{"natIP":"35.1.2.3"}]}]}]}}}`)
		case strings.HasSuffix(r.URL.Path, "/aggregated/addresses"):
			fmt.Fprint(w, `{"items":{"regions/us-central1":{"addresses":[{"name":"static","address":"35.1.2.4","addressType":"EXTERNAL","region":"regions/us-central1","users":["projects/p/zones/us-central1-a/instances/web"]}]}}}`)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	target := model.CloudTarget{Provider: model.CloudGCP, Project: "p", Region: "us-central1"}
	token := "test-token"
	subnets, err := gcpListSubnetworks(context.Background(), server.Client(), server.URL, target, token)
	if err != nil || len(subnets) != 1 || subnets[0].IPCIDRRange != "10.0.0.0/24" {
		t.Fatalf("subnetworks = %#v, err = %v", subnets, err)
	}
	instances, err := gcpListInstances(context.Background(), server.Client(), server.URL, target, token)
	if err != nil || len(instances) != 1 || instances[0].NetworkInterfaces[0].NetworkIP != "10.0.0.10" {
		t.Fatalf("instances = %#v, err = %v", instances, err)
	}
	addresses, err := gcpListAddresses(context.Background(), server.Client(), server.URL, target, token)
	if err != nil || len(addresses) != 1 || addresses[0].Address != "35.1.2.4" {
		t.Fatalf("addresses = %#v, err = %v", addresses, err)
	}
}

func TestVCenterCollect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/session":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `"session-1"`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/session":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/vcenter/vm":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"vm":"vm-1","name":"web","power_state":"POWERED_ON"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/vcenter/vm/vm-1/guest/networking/interfaces":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"nic":"eth0","mac_address":"02:00:00:00:00:01","ip":{"ip_addresses":["10.0.1.10","fe80::1"],"gateway":"10.0.1.1"}}]`)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	target := model.CloudTarget{
		Provider:         model.CloudVCenter,
		Endpoint:         server.URL,
		AccessKey:        "user",
		SecretKey:        "pass",
		CollectInstances: true,
	}
	res, err := Run(context.Background(), target, testLogger(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Instances != 1 {
		t.Errorf("instances = %d, want 1", res.Instances)
	}
	assertAddresses(t, res.Addresses, "10.0.1.10")
}

func TestVCenterExtraSubnets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/session":
			fmt.Fprint(w, `"session-1"`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/vcenter/vm":
			fmt.Fprint(w, `[]`)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	target := model.CloudTarget{
		Provider:        model.CloudVCenter,
		Endpoint:        server.URL,
		AccessKey:       "user",
		SecretKey:       "pass",
		CollectPrefixes: true,
		ExtraSubnets:    "10.0.0.0/16, 2001:db8::/64",
	}
	res, err := Run(context.Background(), target, testLogger(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Prefixes) != 2 {
		t.Fatalf("prefixes = %#v", res.Prefixes)
	}
	found := false
	for _, warning := range res.Warnings {
		if strings.Contains(warning, "vCenter") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected vCenter CIDR warning, got %#v", res.Warnings)
	}
}

func TestParseGCPPrivateKeyRejectsGarbage(t *testing.T) {
	if _, err := parseRSAPrivateKey("not a key"); err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestGCPLastSegment(t *testing.T) {
	cases := map[string]string{
		"projects/p/global/networks/default": "default",
		"regions/us-central1":                "us-central1",
		"default":                            "default",
		"":                                   "",
	}
	for input, want := range cases {
		if got := gcpLastSegment(input); got != want {
			t.Errorf("gcpLastSegment(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCloudResultJSON(t *testing.T) {
	res := model.CloudResult{Provider: model.CloudAWS, Prefixes: []model.CloudPrefix{{CIDR: "10.0.0.0/16"}}}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"prefixes"`) {
		t.Errorf("unexpected JSON: %s", data)
	}
}

func assertAddresses(t *testing.T, addresses []model.CloudAddress, want ...string) {
	t.Helper()
	got := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		got[address.Address] = true
	}
	for _, address := range want {
		if !got[address] {
			t.Errorf("missing address %s in %#v", address, addresses)
		}
	}
	if len(addresses) != len(want) {
		t.Errorf("got %d addresses, want %d: %#v", len(addresses), len(want), addresses)
	}
}
