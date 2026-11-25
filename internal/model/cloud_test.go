package model

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeCloudTarget(t *testing.T) {
	target := CloudTarget{
		Provider:                "  ALIYUN ",
		Endpoint:                " https://ecs.cn-hangzhou.aliyuncs.com/ ",
		ExtraSubnets:            "10.0.0.0/16,10.0.0.0/16 2001:db8::/64",
		ScheduleIntervalMinutes: 1,
	}
	NormalizeCloudTarget(&target)
	if target.Provider != CloudAliyun {
		t.Errorf("provider = %q, want %q", target.Provider, CloudAliyun)
	}
	if target.Endpoint != "https://ecs.cn-hangzhou.aliyuncs.com" {
		t.Errorf("endpoint = %q", target.Endpoint)
	}
	if target.ExtraSubnets != "10.0.0.0/16, 2001:db8::/64" {
		t.Errorf("extra subnets = %q", target.ExtraSubnets)
	}
	if target.ScheduleIntervalMinutes != MinScheduleMinutes {
		t.Errorf("interval = %d, want %d", target.ScheduleIntervalMinutes, MinScheduleMinutes)
	}
	if target.Name == "" {
		t.Error("name should default to a provider label")
	}
}

func TestCloudProviderAliases(t *testing.T) {
	cases := map[string]string{
		"vmware":       CloudVCenter,
		"vSphere":      CloudVCenter,
		"proxmox":      CloudPVE,
		"proxmox-ve":   CloudPVE,
		"alibabacloud": CloudAliyun,
		"qcloud":       CloudTencent,
		"hwc":          CloudHuawei,
		"amazon":       CloudAWS,
		"googlecloud":  CloudGCP,
		"volc":         CloudVolcengine,
	}
	for input, want := range cases {
		target := CloudTarget{Provider: input}
		NormalizeCloudTarget(&target)
		if target.Provider != want {
			t.Errorf("normalize %q = %q, want %q", input, target.Provider, want)
		}
	}
}

func TestValidateCloudTarget(t *testing.T) {
	base := CloudTarget{
		Provider:         CloudAWS,
		Region:           "us-east-1",
		AccessKey:        "ak",
		SecretKey:        "sk",
		CollectInstances: true,
	}
	if err := ValidateCloudTarget(base); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*CloudTarget)
		want   string
	}{
		{"provider", func(t *CloudTarget) { t.Provider = "nope" }, "不支持"},
		{"collect", func(t *CloudTarget) { t.CollectInstances = false }, "至少选择"},
		{"region", func(t *CloudTarget) { t.Region = "" }, "区域"},
		{"keys", func(t *CloudTarget) { t.SecretKey = "" }, "AccessKey"},
		{"endpoint", func(t *CloudTarget) { t.Endpoint = "ftp://example.com" }, "Endpoint"},
		{"subnet", func(t *CloudTarget) { t.ExtraSubnets = "bad-cidr" }, "附加网段"},
	}
	for _, tc := range cases {
		target := base
		tc.mutate(&target)
		err := ValidateCloudTarget(target)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want contains %q", tc.name, err, tc.want)
		}
	}
}

func TestValidateCloudTargetProviderSpecific(t *testing.T) {
	vcenter := CloudTarget{
		Provider:         CloudVCenter,
		CollectInstances: true,
		AccessKey:        "user",
		SecretKey:        "pass",
	}
	if err := ValidateCloudTarget(vcenter); err == nil {
		t.Error("vCenter without endpoint should be rejected")
	}
	vcenter.Endpoint = "https://vcenter.example.com"
	if err := ValidateCloudTarget(vcenter); err != nil {
		t.Errorf("valid vCenter rejected: %v", err)
	}

	pve := CloudTarget{
		Provider:         CloudPVE,
		CollectInstances: true,
		AccessKey:        "root@pam",
		SecretKey:        "pass",
	}
	if err := ValidateCloudTarget(pve); err == nil {
		t.Error("PVE without endpoint should be rejected")
	}
	pve.Endpoint = "https://pve.example.com:8006"
	if err := ValidateCloudTarget(pve); err != nil {
		t.Errorf("valid PVE rejected: %v", err)
	}
	pve.AccessKey = "root@pam!glean"
	if err := ValidateCloudTarget(pve); err != nil {
		t.Errorf("valid PVE API token rejected: %v", err)
	}
	for _, user := range []string{"root", "root@pam!"} {
		pve.AccessKey = user
		if err := ValidateCloudTarget(pve); err == nil || !strings.Contains(err.Error(), "user@realm") {
			t.Errorf("PVE user %q: err = %v, want format error", user, err)
		}
	}
	pve.AccessKey = "root@pam"
	pve.CollectInstances = false
	pve.CollectPrefixes = true
	if err := ValidateCloudTarget(pve); err == nil || !strings.Contains(err.Error(), "附加网段") {
		t.Errorf("PVE without instances or extra subnets: %v", err)
	}
	pve.ExtraSubnets = "10.0.0.0/16"
	if err := ValidateCloudTarget(pve); err != nil {
		t.Errorf("PVE with extra subnets rejected: %v", err)
	}

	gcp := CloudTarget{Provider: CloudGCP, CollectInstances: true}
	if err := ValidateCloudTarget(gcp); err == nil || !strings.Contains(err.Error(), "Project") {
		t.Errorf("GCP without project: %v", err)
	}
	gcp.Project = "demo"
	if err := ValidateCloudTarget(gcp); err == nil || !strings.Contains(err.Error(), "服务账号") {
		t.Errorf("GCP without credentials: %v", err)
	}
	gcp.Credentials = `{"type":"service_account"}`
	if err := ValidateCloudTarget(gcp); err != nil {
		t.Errorf("valid GCP rejected: %v", err)
	}

	huawei := CloudTarget{
		Provider:         CloudHuawei,
		Region:           "cn-north-4",
		AccessKey:        "ak",
		SecretKey:        "sk",
		CollectInstances: true,
	}
	if err := ValidateCloudTarget(huawei); err == nil || !strings.Contains(err.Error(), "Project") {
		t.Errorf("Huawei without project: %v", err)
	}
	huawei.Project = "proj"
	if err := ValidateCloudTarget(huawei); err != nil {
		t.Errorf("valid Huawei rejected: %v", err)
	}
}

func TestCloudTargetSchedule(t *testing.T) {
	target := CloudTarget{
		Enabled:                 true,
		ScheduleEnabled:         true,
		ScheduleIntervalMinutes: 30,
	}
	if !target.ScheduleDue(time.Now()) {
		t.Error("never-run target should be due")
	}
	last := time.Now().Add(-10 * time.Minute)
	target.LastRunAt = &last
	if target.ScheduleDue(time.Now()) {
		t.Error("target should not be due before the interval")
	}
	if target.ScheduleInterval() != 30*time.Minute {
		t.Errorf("interval = %v, want 30m", target.ScheduleInterval())
	}
	target.Enabled = false
	if target.ScheduleDue(time.Now()) {
		t.Error("disabled target must not be due")
	}
}
