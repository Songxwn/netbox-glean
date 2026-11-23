package model

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Supported cloud providers.
const (
	CloudVCenter    = "vcenter"
	CloudAliyun     = "aliyun"
	CloudTencent    = "tencent"
	CloudHuawei     = "huawei"
	CloudAWS        = "aws"
	CloudGCP        = "gcp"
	CloudVolcengine = "volcengine"

	// ProtocolCloud is the pseudo protocol used by cloud asset jobs.
	ProtocolCloud = "cloud"
)

// Cloud target kinds recorded in NetBox prefix descriptions.
const (
	CloudPrefixVPC     = "vpc"
	CloudPrefixSubnet  = "subnet"
	CloudPrefixSwitch  = "switch"
	CloudPrefixManual  = "manual"
	CloudAddressVM     = "instance"
	CloudAddressPublic = "eip"
)

// CloudProviderLabel returns the human readable provider name.
func CloudProviderLabel(provider string) string {
	switch normalizeCloudProvider(provider) {
	case CloudVCenter:
		return "VMware vCenter"
	case CloudAliyun:
		return "阿里云"
	case CloudTencent:
		return "腾讯云"
	case CloudHuawei:
		return "华为云"
	case CloudAWS:
		return "AWS"
	case CloudGCP:
		return "Google Cloud"
	case CloudVolcengine:
		return "火山引擎"
	default:
		return provider
	}
}

func normalizeCloudProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "vcenter", "vmware", "vsphere":
		return CloudVCenter
	case "aliyun", "alibaba", "alibabacloud", "ali":
		return CloudAliyun
	case "tencent", "qcloud", "tencentcloud":
		return CloudTencent
	case "huawei", "huaweicloud", "hwc":
		return CloudHuawei
	case "aws", "amazon", "amazonaws":
		return CloudAWS
	case "gcp", "google", "googlecloud":
		return CloudGCP
	case "volcengine", "volc", "volces", "volcengineapi":
		return CloudVolcengine
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

// CloudTarget is an independent cloud asset discovery job. It reads VPC and
// subnet ranges plus instance and elastic IP addresses from a cloud API and
// writes them into NetBox IPAM.
type CloudTarget struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	// Endpoint overrides the provider API endpoint. It is required for
	// vCenter and optional for the public cloud providers (useful for
	// private endpoints or compatible APIs).
	Endpoint string `json:"endpoint,omitempty"`
	Region   string `json:"region,omitempty"`
	// AccessKey is the access key id, secret id or vCenter user name.
	AccessKey string `json:"access_key,omitempty"`
	// SecretKey is the access key secret, secret key or vCenter password.
	SecretKey string `json:"secret_key,omitempty"`
	// Project is the GCP project id or the Huawei Cloud project id.
	Project string `json:"project,omitempty"`
	// Credentials holds the GCP service account JSON. Other providers use
	// AccessKey/SecretKey.
	Credentials string `json:"credentials,omitempty"`
	Insecure    bool   `json:"insecure,omitempty"`
	// CollectPrefixes writes VPC/subnet ranges to NetBox IPAM prefixes.
	CollectPrefixes  bool `json:"collect_prefixes"`
	CollectInstances bool `json:"collect_instances"`
	CollectPublicIPs bool `json:"collect_public_ips"`
	// ExtraSubnets adds manually maintained CIDRs (for example vCenter
	// portgroup subnets, which the vCenter API does not expose).
	ExtraSubnets string `json:"extra_subnets,omitempty"`
	// Site overrides the global NetBox site slug for this account.
	Site string `json:"site,omitempty"`
	// ScheduleEnabled turns on periodic cloud discovery + IPAM sync.
	ScheduleEnabled         bool       `json:"schedule_enabled"`
	ScheduleIntervalMinutes int        `json:"schedule_interval_minutes,omitempty"`
	LastRunAt               *time.Time `json:"last_run_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// NormalizeCloudTarget fills in defaults and strips accidental whitespace.
func NormalizeCloudTarget(t *CloudTarget) {
	t.Name = strings.TrimSpace(t.Name)
	t.Provider = normalizeCloudProvider(t.Provider)
	t.Endpoint = strings.TrimSpace(t.Endpoint)
	t.Endpoint = strings.TrimRight(t.Endpoint, "/")
	t.Region = strings.TrimSpace(t.Region)
	t.AccessKey = strings.TrimSpace(t.AccessKey)
	t.SecretKey = strings.TrimSpace(t.SecretKey)
	t.Project = strings.TrimSpace(t.Project)
	t.Credentials = strings.TrimSpace(t.Credentials)
	t.ExtraSubnets = NormalizeSubnets(t.ExtraSubnets)
	t.Site = strings.TrimSpace(t.Site)
	t.ScheduleIntervalMinutes = clampScheduleMinutes(t.ScheduleIntervalMinutes)
	if t.Name == "" {
		t.Name = First(t.Project, t.Region, CloudProviderLabel(t.Provider), "云资产采集")
	}
}

// ExtraCIDRs returns the manually configured prefix list.
func (t CloudTarget) ExtraCIDRs() []string {
	return SplitList(t.ExtraSubnets)
}

// ScheduleInterval returns the normalized interval between scheduled runs.
func (t CloudTarget) ScheduleInterval() time.Duration {
	return normalizeScheduleMinutes(t.ScheduleIntervalMinutes)
}

// ScheduleDue reports whether the cloud target needs a scheduled run at now.
func (t CloudTarget) ScheduleDue(now time.Time) bool {
	if !t.Enabled || !t.ScheduleEnabled {
		return false
	}
	if t.LastRunAt == nil || t.LastRunAt.IsZero() {
		return true
	}
	return now.Sub(*t.LastRunAt) >= t.ScheduleInterval()
}

// ValidateCloudTarget checks the credentials and options of a cloud target.
func ValidateCloudTarget(t CloudTarget) error {
	switch t.Provider {
	case CloudVCenter, CloudAliyun, CloudTencent, CloudHuawei, CloudAWS, CloudGCP, CloudVolcengine:
	default:
		return fmt.Errorf("不支持的云平台 %q", t.Provider)
	}
	if t.Endpoint != "" {
		u, err := url.Parse(t.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("无效的 API Endpoint %q，请填写完整 URL", t.Endpoint)
		}
	}
	if !t.CollectPrefixes && !t.CollectInstances && !t.CollectPublicIPs {
		return fmt.Errorf("请至少选择一种采集内容")
	}
	if err := validatePrefixCIDRs(t.ExtraCIDRs()); err != nil {
		return err
	}
	switch t.Provider {
	case CloudVCenter:
		if t.Endpoint == "" {
			return fmt.Errorf("vCenter 需要填写 API Endpoint，例如 https://vcenter.example.com")
		}
		if t.AccessKey == "" || t.SecretKey == "" {
			return fmt.Errorf("vCenter 需要填写用户名和密码")
		}
		if !t.CollectInstances && len(t.ExtraCIDRs()) == 0 {
			return fmt.Errorf("vCenter 通过 Guest IP 或附加网段采集，请至少勾选「实例 IP」或填写附加网段")
		}
	case CloudGCP:
		if t.Project == "" {
			return fmt.Errorf("Google Cloud 需要填写 Project ID")
		}
		if t.Credentials == "" {
			return fmt.Errorf("Google Cloud 需要填写服务账号 JSON 凭据")
		}
	default:
		if t.Region == "" {
			return fmt.Errorf("%s 需要填写区域（Region）", CloudProviderLabel(t.Provider))
		}
		if t.Provider == CloudHuawei && t.Project == "" {
			return fmt.Errorf("华为云需要填写 Project ID")
		}
		if t.AccessKey == "" || t.SecretKey == "" {
			return fmt.Errorf("%s 需要填写 AccessKey 与 SecretKey", CloudProviderLabel(t.Provider))
		}
	}
	return nil
}

// validatePrefixCIDRs accepts any valid IPv4 or IPv6 CIDR. Cloud VPC and
// subnet ranges are recorded in NetBox IPAM and are not enumerated, so wide
// IPv6 prefixes such as /64 are allowed here.
func validatePrefixCIDRs(cidrs []string) error {
	for _, cidr := range cidrs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("无效的附加网段 %q: %w", cidr, err)
		}
	}
	return nil
}

// CloudPrefix is a VPC, subnet, switch or manually maintained network range.
type CloudPrefix struct {
	CIDR     string `json:"cidr"`
	Name     string `json:"name,omitempty"`
	VPC      string `json:"vpc,omitempty"`
	Region   string `json:"region,omitempty"`
	Kind     string `json:"kind,omitempty"` // vpc, subnet, switch or manual
	Provider string `json:"provider,omitempty"`
}

// CloudAddress is an address used by an instance or a public/elastic IP.
type CloudAddress struct {
	Address    string `json:"address"`
	Name       string `json:"name,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Interface  string `json:"interface,omitempty"`
	MAC        string `json:"mac,omitempty"`
	Public     bool   `json:"public,omitempty"`
	Kind       string `json:"kind,omitempty"` // instance or eip
	Region     string `json:"region,omitempty"`
	VPC        string `json:"vpc,omitempty"`
}

// CloudResult is the normalized output of one cloud asset discovery run.
type CloudResult struct {
	Provider    string         `json:"provider"`
	CollectedAt time.Time      `json:"collected_at"`
	Prefixes    []CloudPrefix  `json:"prefixes,omitempty"`
	Addresses   []CloudAddress `json:"addresses,omitempty"`
	VPCs        int            `json:"vpcs"`
	Subnets     int            `json:"subnets"`
	Instances   int            `json:"instances"`
	PublicIPs   int            `json:"public_ips"`
	Warnings    []string       `json:"warnings,omitempty"`
}

// AddWarning appends a non-fatal warning once.
func (r *CloudResult) AddWarning(format string, args ...any) {
	message := Sprintf(format, args...)
	if len(r.Warnings) >= 30 {
		return
	}
	for _, existing := range r.Warnings {
		if existing == message {
			return
		}
	}
	r.Warnings = append(r.Warnings, message)
}
