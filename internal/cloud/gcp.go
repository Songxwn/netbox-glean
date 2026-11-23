package cloud

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
)

const (
	gcpScope           = "https://www.googleapis.com/auth/compute.readonly"
	gcpDefaultTokenURI = "https://oauth2.googleapis.com/token"
	gcpDefaultBase     = "https://compute.googleapis.com"
)

type gcpCredentials struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

type gcpSubnetwork struct {
	Name              string `json:"name"`
	Network           string `json:"network"`
	IPCIDRRange       string `json:"ipCidrRange"`
	Region            string `json:"region"`
	SecondaryIPRanges []struct {
		RangeName   string `json:"rangeName"`
		IPCIDRRange string `json:"ipCidrRange"`
	} `json:"secondaryIpRanges"`
}

type gcpInstance struct {
	Name              string `json:"name"`
	ID                uint64 `json:"id"`
	NetworkInterfaces []struct {
		Network       string `json:"network"`
		Subnetwork    string `json:"subnetwork"`
		NetworkIP     string `json:"networkIP"`
		IPv6Address   string `json:"ipv6Address"`
		AccessConfigs []struct {
			NatIP string `json:"natIP"`
			Type  string `json:"type"`
		} `json:"accessConfigs"`
		IPv6AccessConfigs []struct {
			ExternalIPv6 string `json:"externalIpv6"`
		} `json:"ipv6AccessConfigs"`
	} `json:"networkInterfaces"`
}

type gcpAddress struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	AddressType string   `json:"addressType"`
	Status      string   `json:"status"`
	Region      string   `json:"region"`
	Users       []string `json:"users"`
}

type gcpTokenEntry struct {
	token   string
	expires time.Time
}

var gcpTokens sync.Map

func collectGCP(ctx context.Context, hc *http.Client, target model.CloudTarget, logf model.LogFunc) (*model.CloudResult, error) {
	token, err := gcpAccessToken(ctx, hc, target.Credentials, time.Now())
	if err != nil {
		return nil, fmt.Errorf("获取 Google Cloud 访问令牌失败: %w", err)
	}
	res := &model.CloudResult{Provider: target.Provider, CollectedAt: time.Now().UTC()}
	base := endpointOrDefault(target.Endpoint, gcpDefaultBase)

	if target.CollectPrefixes {
		subnets, err := gcpListSubnetworks(ctx, hc, base, target, token)
		if err != nil {
			return nil, fmt.Errorf("查询 Google Cloud 子网失败: %w", err)
		}
		res.Subnets = len(subnets)
		networks := map[string]bool{}
		for _, subnet := range subnets {
			vpc := gcpLastSegment(subnet.Network)
			if vpc != "" {
				networks[vpc] = true
			}
			name := model.First(subnet.Name, subnet.IPCIDRRange)
			res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: subnet.IPCIDRRange, Name: name, VPC: vpc, Region: gcpLastSegment(subnet.Region), Kind: model.CloudPrefixSubnet, Provider: target.Provider})
			for _, secondary := range subnet.SecondaryIPRanges {
				secondaryName := name
				if secondary.RangeName != "" {
					secondaryName += " / " + secondary.RangeName
				}
				res.Prefixes = append(res.Prefixes, model.CloudPrefix{CIDR: secondary.IPCIDRRange, Name: secondaryName, VPC: vpc, Region: gcpLastSegment(subnet.Region), Kind: model.CloudPrefixSubnet, Provider: target.Provider})
			}
		}
		res.VPCs = len(networks)
		logf("Google Cloud 子网: %d 个（VPC %d 个）", len(subnets), len(networks))
	}

	if target.CollectInstances {
		instances, err := gcpListInstances(ctx, hc, base, target, token)
		if err != nil {
			return nil, fmt.Errorf("查询 Google Cloud 实例失败: %w", err)
		}
		res.Instances = len(instances)
		for _, instance := range instances {
			name := model.First(instance.Name, strconv.FormatUint(instance.ID, 10))
			for _, nic := range instance.NetworkInterfaces {
				vpc := gcpLastSegment(nic.Network)
				if nic.NetworkIP != "" {
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: nic.NetworkIP, Name: name, InstanceID: name, Interface: gcpLastSegment(nic.Subnetwork), Kind: model.CloudAddressVM, Region: target.Region, VPC: vpc})
				}
				if nic.IPv6Address != "" {
					res.Addresses = append(res.Addresses, model.CloudAddress{Address: nic.IPv6Address, Name: name, InstanceID: name, Interface: gcpLastSegment(nic.Subnetwork), Kind: model.CloudAddressVM, Region: target.Region, VPC: vpc})
				}
				if target.CollectPublicIPs {
					for _, access := range nic.AccessConfigs {
						if access.NatIP != "" {
							res.Addresses = append(res.Addresses, model.CloudAddress{Address: access.NatIP, Name: name, InstanceID: name, Public: true, Kind: model.CloudAddressPublic, Region: target.Region, VPC: vpc})
						}
					}
					for _, access := range nic.IPv6AccessConfigs {
						if access.ExternalIPv6 != "" {
							res.Addresses = append(res.Addresses, model.CloudAddress{Address: access.ExternalIPv6, Name: name, InstanceID: name, Public: true, Kind: model.CloudAddressPublic, Region: target.Region, VPC: vpc})
						}
					}
				}
			}
		}
		logf("Google Cloud 实例: %d 个", len(instances))
	}

	if target.CollectPublicIPs {
		addresses, err := gcpListAddresses(ctx, hc, base, target, token)
		if err != nil {
			return nil, fmt.Errorf("查询 Google Cloud 静态地址失败: %w", err)
		}
		for _, address := range addresses {
			// The aggregated addresses API also returns internal addresses,
			// which are usually already collected from instance interfaces.
			if address.Address == "" || !strings.EqualFold(address.AddressType, "EXTERNAL") {
				continue
			}
			instance := ""
			if len(address.Users) > 0 {
				instance = gcpLastSegment(address.Users[0])
			}
			res.Addresses = append(res.Addresses, model.CloudAddress{Address: address.Address, Name: model.First(address.Name, instance), InstanceID: instance, Public: true, Kind: model.CloudAddressPublic, Region: gcpLastSegment(address.Region)})
		}
		logf("Google Cloud 静态地址: %d 个", len(addresses))
	}
	return res, nil
}

func gcpLastSegment(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return ""
	}
	if i := strings.LastIndexByte(value, '/'); i >= 0 {
		return value[i+1:]
	}
	return value
}

func gcpAccessToken(ctx context.Context, hc *http.Client, credentialsJSON string, now time.Time) (string, error) {
	var creds gcpCredentials
	if err := json.Unmarshal([]byte(credentialsJSON), &creds); err != nil {
		return "", fmt.Errorf("服务账号 JSON 无效: %w", err)
	}
	if creds.ClientEmail == "" || creds.PrivateKey == "" {
		return "", fmt.Errorf("服务账号 JSON 缺少 client_email 或 private_key")
	}
	tokenURI := model.First(creds.TokenURI, gcpDefaultTokenURI)
	cacheKey := creds.ClientEmail + "|" + tokenURI
	if cached, ok := gcpTokens.Load(cacheKey); ok {
		entry := cached.(gcpTokenEntry)
		if now.Add(60 * time.Second).Before(entry.expires) {
			return entry.token, nil
		}
	}

	key, err := parseRSAPrivateKey(creds.PrivateKey)
	if err != nil {
		return "", err
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss":   creds.ClientEmail,
		"scope": gcpScope,
		"aud":   tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("签名服务账号 JWT 失败: %w", err)
	}
	assertion := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return "", err
	}
	var resp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("解析 Google OAuth 响应失败: %w", err)
	}
	if resp.AccessToken == "" {
		return "", fmt.Errorf("Google OAuth 未返回 access_token")
	}
	if resp.ExpiresIn <= 0 {
		resp.ExpiresIn = 3600
	}
	gcpTokens.Store(cacheKey, gcpTokenEntry{token: resp.AccessToken, expires: now.Add(time.Duration(resp.ExpiresIn) * time.Second)})
	return resp.AccessToken, nil
}

func parseRSAPrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, fmt.Errorf("服务账号私钥不是有效的 PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("无法解析服务账号 RSA 私钥")
}

func gcpGet(ctx context.Context, hc *http.Client, base, project, token, resource string, query url.Values, out any) error {
	u := cleanBase(base) + "/compute/v1/projects/" + url.PathEscape(project) + "/" + resource
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	data, err := doRequest(ctx, hc, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析 Google Cloud 响应失败: %w", err)
	}
	return nil
}

type gcpPage struct {
	NextPageToken string `json:"nextPageToken"`
}

func gcpListSubnetworks(ctx context.Context, hc *http.Client, base string, target model.CloudTarget, token string) ([]gcpSubnetwork, error) {
	var out []gcpSubnetwork
	pageToken := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Items map[string]struct {
				Subnetworks []gcpSubnetwork `json:"subnetworks"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		q := url.Values{"maxResults": {"500"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		if err := gcpGet(ctx, hc, base, target.Project, token, "aggregated/subnetworks", q, &resp); err != nil {
			return nil, err
		}
		for _, scope := range resp.Items {
			out = append(out, scope.Subnetworks...)
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out, nil
}

func gcpListInstances(ctx context.Context, hc *http.Client, base string, target model.CloudTarget, token string) ([]gcpInstance, error) {
	var out []gcpInstance
	pageToken := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Items map[string]struct {
				Instances []gcpInstance `json:"instances"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		q := url.Values{"maxResults": {"500"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		if err := gcpGet(ctx, hc, base, target.Project, token, "aggregated/instances", q, &resp); err != nil {
			return nil, err
		}
		for _, scope := range resp.Items {
			out = append(out, scope.Instances...)
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out, nil
}

func gcpListAddresses(ctx context.Context, hc *http.Client, base string, target model.CloudTarget, token string) ([]gcpAddress, error) {
	var out []gcpAddress
	pageToken := ""
	for page := 0; page < maxPages; page++ {
		var resp struct {
			Items map[string]struct {
				Addresses []gcpAddress `json:"addresses"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		q := url.Values{"maxResults": {"500"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		if err := gcpGet(ctx, hc, base, target.Project, token, "aggregated/addresses", q, &resp); err != nil {
			return nil, err
		}
		for _, scope := range resp.Items {
			out = append(out, scope.Addresses...)
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out, nil
}
