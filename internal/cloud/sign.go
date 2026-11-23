package cloud

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// awsEncode percent-encodes every byte except the RFC 3986 unreserved set.
// It is used by the AWS, Alibaba Cloud, Huawei Cloud and Volcengine
// signatures, which all share that canonical encoding rule.
func awsEncode(value string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z',
			c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&0x0f])
		}
	}
	return b.String()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func hmacSHA1(key, data []byte) []byte {
	mac := hmac.New(sha1.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// signAWS signs an EC2 Query API request with AWS Signature Version 4.
func signAWS(req *http.Request, body []byte, accessKey, secretKey, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)

	contentType := req.Header.Get("Content-Type")
	host := req.URL.Host
	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + host + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "content-type;host;x-amz-date"

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonicalRequest := req.Method + "\n" +
		path + "\n" +
		encodeQuery(req.URL.Query()) + "\n" +
		canonicalHeaders + "\n" +
		signedHeaders + "\n" +
		sha256Hex(body)

	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// aliyunURL builds a signed Alibaba Cloud RPC API URL.
func aliyunURL(endpoint, version, action string, params url.Values, accessKey, secretKey string, now time.Time) string {
	q := url.Values{}
	for key, values := range params {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	q.Set("AccessKeyId", accessKey)
	q.Set("Action", action)
	q.Set("Format", "JSON")
	q.Set("SignatureMethod", "HMAC-SHA1")
	q.Set("SignatureNonce", randomHex(16))
	q.Set("SignatureVersion", "1.0")
	q.Set("Timestamp", now.UTC().Format("2006-01-02T15:04:05Z"))
	q.Set("Version", version)
	canonical := encodeQuery(q)
	stringToSign := "GET&%2F&" + awsEncode(canonical)
	signature := base64.StdEncoding.EncodeToString(hmacSHA1([]byte(secretKey+"&"), []byte(stringToSign)))
	return cleanBase(endpoint) + "/?" + canonical + "&Signature=" + awsEncode(signature)
}

// signTencent signs a Tencent Cloud API v3 request (TC3-HMAC-SHA256).
func signTencent(req *http.Request, body []byte, accessKey, secretKey, region, service, action string, now time.Time) {
	now = now.UTC()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	date := now.Format("2006-01-02")
	contentType := "application/json; charset=utf-8"
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Timestamp", timestamp)
	req.Header.Set("X-TC-Region", region)

	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + req.URL.Host + "\n" +
		"x-tc-action:" + strings.ToLower(action) + "\n"
	signedHeaders := "content-type;host;x-tc-action"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + sha256Hex(body)

	scope := date + "/" + service + "/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + timestamp + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	kDate := hmacSHA256([]byte("TC3"+secretKey), []byte(date))
	kService := hmacSHA256(kDate, []byte(service))
	kSigning := hmacSHA256(kService, []byte("tc3_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", "TC3-HMAC-SHA256 Credential="+accessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// signHuawei signs a Huawei Cloud request (SDK-HMAC-SHA256).
func signHuawei(req *http.Request, body []byte, accessKey, secretKey string, now time.Time) {
	date := now.UTC().Format("20060102T150405Z")
	req.Header.Set("X-Sdk-Date", date)

	canonicalQuery := req.URL.RawQuery
	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-sdk-date:" + date + "\n"
	signedHeaders := "host;x-sdk-date"
	path := req.URL.Path
	if path == "" {
		path = "/"
	}
	canonicalRequest := req.Method + "\n" +
		path + "\n" +
		canonicalQuery + "\n" +
		canonicalHeaders + "\n" +
		signedHeaders + "\n" +
		sha256Hex(body)
	stringToSign := "SDK-HMAC-SHA256\n" + date + "\n" + sha256Hex([]byte(canonicalRequest))
	signature := hex.EncodeToString(hmacSHA256([]byte(secretKey), []byte(stringToSign)))

	req.Header.Set("Authorization", "SDK-HMAC-SHA256 Access="+accessKey+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// signVolcengine signs a Volcengine API request (HMAC-SHA256 V4 style).
func signVolcengine(req *http.Request, body []byte, accessKey, secretKey, region, service string, now time.Time) {
	now = now.UTC()
	date := now.Format("20060102")
	xDate := now.Format("20060102T150405Z")
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", payloadHash)

	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-content-sha256:" + payloadHash + "\n" +
		"x-date:" + xDate + "\n"
	signedHeaders := "host;x-content-sha256;x-date"
	path := req.URL.Path
	if path == "" {
		path = "/"
	}
	canonicalRequest := req.Method + "\n" +
		path + "\n" +
		encodeQuery(req.URL.Query()) + "\n" +
		canonicalHeaders + "\n" +
		signedHeaders + "\n" +
		payloadHash

	scope := date + "/" + region + "/" + service + "/request"
	stringToSign := "HMAC-SHA256\n" + xDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	kDate := hmacSHA256([]byte(secretKey), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", "HMAC-SHA256 Credential="+accessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// sortedValues returns the values of q[key] in a stable order.
func sortedValues(q url.Values, key string) []string {
	values := append([]string(nil), q[key]...)
	sort.Strings(values)
	return values
}
