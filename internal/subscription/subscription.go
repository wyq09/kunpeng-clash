// Package subscription 实现订阅（远程配置）的拉取与解析。
//
// 契约见 DESIGN.md §5 ProfilesService：UA 固定为
// clash.meta/1.19.0 KunpengClash/0.0.1；解析 subscription-userinfo 流量头
// 与 YAML proxies 节点数量、content-disposition 订阅名。
package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// UserAgent 订阅请求 UA（机场据此返回 clash YAML 而非 base64 节点列表）。
const UserAgent = "clash.meta/1.19.0 KunpengClash/0.0.1"

// UserInfo 订阅流量信息（subscription-userinfo 头）。
type UserInfo struct {
	Upload   uint64    `json:"upload"`
	Download uint64    `json:"download"`
	Total    uint64    `json:"total"`
	Expire   time.Time `json:"expire"` // 零值 = 无期限
}

// Result 一次订阅拉取的结果。
type Result struct {
	Raw       []byte   // 订阅原文（YAML）
	UserInfo  UserInfo // 流量信息（可能为零值）
	Name      string   // 从 content-disposition 解析的订阅名（可为空）
	NodeCount int      // proxies 节点数
}

// fetchClient 直连客户端：显式 Transport.Proxy = nil，
// 防止继承用户 shell 里残留的代理环境变量。
var fetchClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy:                 nil,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	},
}

// Fetch 拉取订阅并解析流量头与节点数。
// directClient 失败（DNS 解析失败/直连网络故障）时，若 proxyAddr 非空则
// 经本地内核混合端口自动重试一次——机场订阅域名被墙内 DNS 污染但节点
// 服务器域名仍可用时，远端解析往往仍能命中。
func Fetch(ctx context.Context, rawURL string) (*Result, error) {
	raw, hdr, err := fetchOnce(ctx, rawURL, "")
	if err == nil {
		return parseResult(raw, hdr)
	}
	directErr := err
	if proxyAddr != "" {
		if raw, hdr, perr := fetchOnce(ctx, rawURL, proxyAddr); perr == nil {
			log.Printf("[kunpeng][subscription] 直连拉取失败（%v），已改经本地内核重试成功", directErr)
			return parseResult(raw, hdr)
		}
	}
	// 两次都失败：把 DNS 类失败翻译成人话
	if isDNSError(directErr) {
		return nil, fmt.Errorf("订阅域名解析失败——机场域名可能已故障或被墙（可尝试编辑订阅为新地址，或稍后重试）: %w", directErr)
	}
	return nil, directErr
}

// proxyAddr 本地内核混合端口地址（如 127.0.0.1:7890），空 = 禁用兜底。
var proxyAddr string

// SetProxyFallback 设置订阅拉取的经代理兜底地址。
func SetProxyFallback(addr string) { proxyAddr = addr }

func isDNSError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "resolve") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout")
}

func fetchOnce(ctx context.Context, rawURL, viaProxy string) ([]byte, http.Header, error) {
	if _, err := url.Parse(rawURL); err != nil {
		return nil, nil, fmt.Errorf("订阅地址无效: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("订阅地址无效: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "*/*")

	client := fetchClient
	if viaProxy != "" {
		pu := &url.URL{Scheme: "http", Host: viaProxy}
		client = &http.Client{
			Timeout:   fetchClient.Timeout,
			Transport: &http.Transport{Proxy: http.ProxyURL(pu), TLSClientConfig: fetchClient.Transport.(*http.Transport).TLSClientConfig},
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("拉取订阅失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, nil, fmt.Errorf("拉取订阅失败: HTTP %d %s", resp.StatusCode, resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32MB 上限防滥用
	if err != nil {
		return nil, nil, fmt.Errorf("读取订阅内容失败: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("订阅内容为空")
	}

	return raw, resp.Header, nil
}

// parseResult 从订阅原文与响应头解析流量信息/文件名/节点数。
func parseResult(raw []byte, hdr http.Header) (*Result, error) {
	res := &Result{Raw: raw, Name: fileNameFromDisposition(hdr.Get("Content-Disposition"))}
	res.UserInfo = ParseUserInfo(hdr.Get("subscription-userinfo"))
	nodeCount, err := CountProxies(raw)
	if err != nil {
		return nil, fmt.Errorf("解析订阅 YAML 失败: %w", err)
	}
	res.NodeCount = nodeCount
	return res, nil
}

// ParseUserInfo 解析 `upload=;download=;total;expire=` 头
// （分号分隔，秒级 Unix 时间戳；缺项/非法项按 0 处理）。
func ParseUserInfo(header string) UserInfo {
	var info UserInfo
	for _, part := range strings.Split(header, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		var n uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(kv[1]), "%d", &n); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "upload":
			info.Upload = n
		case "download":
			info.Download = n
		case "total":
			info.Total = n
		case "expire":
			if n > 0 {
				info.Expire = time.Unix(int64(n), 0)
			}
		}
	}
	return info
}

// CountProxies 解析 YAML，返回 proxies 节点数量。
func CountProxies(raw []byte) (int, error) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return 0, err
	}
	return len(doc.Proxies), nil
}

// ProxyNames 解析 YAML，按顺序返回全部节点名（可能重复）。
func ProxyNames(raw []byte) ([]string, error) {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(doc.Proxies))
	for _, p := range doc.Proxies {
		if n, ok := p["name"].(string); ok && n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// fileNameFromDisposition 从 Content-Disposition 提取文件名（订阅名）。
// 优先 filename*（RFC 5987），回退 filename；失败返回 ""。
func fileNameFromDisposition(header string) string {
	if header == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(header); err == nil {
		if fn := params["filename*"]; fn != "" {
			if name, err := decodeRFC5987(fn); err == nil && name != "" {
				return name
			}
		}
		if fn := params["filename"]; fn != "" {
			return strings.Trim(fn, "\"")
		}
	}
	return ""
}

// decodeRFC5987 解码 filename*=UTF-8”xxx 形式。
func decodeRFC5987(v string) (string, error) {
	if i := strings.IndexByte(v, '\''); i >= 0 {
		parts := strings.SplitN(v, "'", 3)
		if len(parts) == 3 {
			return url.QueryUnescape(parts[2])
		}
	}
	return url.QueryUnescape(v)
}

// CompactInfo 把 UserInfo 转成便于日志/测试的紧凑形式。
func (u UserInfo) String() string {
	b, _ := json.Marshal(u)
	return string(b)
}
