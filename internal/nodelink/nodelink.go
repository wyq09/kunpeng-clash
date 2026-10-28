// Package nodelink 解析节点分享链接（vless:// vmess:// ss:// hysteria2:// trojan://），
// 转换为 mihomo proxies 配置项（字段名与 mihomo v1.19.33 adapter/outbound 各 Option
// 的 proxy 标签一致）。
//
// 解析是尽力而为：无法识别的行跳过并计入 failed，不中断整批。
package nodelink

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Parse 按行解析分享链接，返回 (成功解析的 mihomo proxy 配置列表, 成功数, 失败数)。
// 空行/注释行直接忽略（不计入失败）；fragment 缺失时自动命名「自定义节点N」。
func Parse(raw string) ([]map[string]any, int, int) {
	proxies := make([]map[string]any, 0, 8)
	autoIdx := 0
	ok, failed := 0, 0
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		p, err := parseLine(line)
		if err != nil {
			failed++
			continue
		}
		if p["name"].(string) == "" {
			autoIdx++
			p["name"] = fmt.Sprintf("自定义节点%d", autoIdx)
		}
		proxies = append(proxies, p)
		ok++
	}
	return proxies, ok, failed
}

// parseLine 解析单行链接，按 scheme 分发。
func parseLine(line string) (map[string]any, error) {
	scheme, rest, found := strings.Cut(line, "://")
	if !found {
		return nil, fmt.Errorf("缺少 :// 前缀")
	}
	scheme = strings.ToLower(scheme)
	switch scheme {
	case "vless":
		return parseVLESS(rest)
	case "vmess":
		return parseVMESS(rest)
	case "ss":
		return parseSS(rest)
	case "hysteria2", "hy2":
		return parseHysteria2(rest)
	case "trojan":
		return parseTrojan(rest)
	default:
		return nil, fmt.Errorf("不支持的协议: %s", scheme)
	}
}

// ---------------------------------------------------------------------------
// 通用小工具
// ---------------------------------------------------------------------------

// splitFragment 去掉 "#名称" 片段并 URL 解码；返回剩余部分与节点名（可为空）。
func splitFragment(s string) (string, string) {
	body, name, _ := strings.Cut(s, "#")
	if dec, err := url.QueryUnescape(name); err == nil {
		name = dec
	}
	return body, strings.TrimSpace(name)
}

// parseQuery 解析 "k=v&k=v" 查询串（URL 解码），保留每个键的首次出现值。
func parseQuery(q string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(q, "&") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		if kd, err := url.QueryUnescape(k); err == nil {
			k = kd
		}
		if vd, err := url.QueryUnescape(v); err == nil {
			v = vd
		}
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}

// splitHostPort 拆 host:port，缺省端口用 def；解析失败返回错误。
func splitHostPort(hostport string, def int) (string, int, error) {
	hostport = strings.TrimSpace(hostport)
	hostport = strings.TrimSuffix(hostport, "/")
	h, p, err := net.SplitHostPort(hostport)
	if err != nil {
		// 无端口：整段是 host（裸 IPv6 带多冒号的情形按不支持处理）。
		if strings.Contains(hostport, ":") {
			return "", 0, fmt.Errorf("无法解析地址: %s", hostport)
		}
		return strings.Trim(hostport, "[]"), def, nil
	}
	port, perr := strconv.Atoi(p)
	if perr != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("非法端口: %s", p)
	}
	return strings.Trim(h, "[]"), port, nil
}

// b64Decode 容错 base64 解码：标准 / URL-safe / 无 padding 全部尝试。
func b64Decode(s string) ([]byte, bool) {
	s = strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(strings.TrimSpace(s))
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

// firstOf 返回查询串中第一个非空的键值。
func firstOf(q map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := q[k]; v != "" {
			return v
		}
	}
	return ""
}

// toInt 宽松转换 JSON 里的 port/aid（可能是字符串或数字）。
func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil
	case int:
		return x, true
	default:
		return 0, false
	}
}

// tlsTransportOpts 把 type/path/host/serviceName 查询参数映射为
// network 与 ws-opts/grpc-opts（vless/trojan 共用）。
func tlsTransportOpts(p map[string]any, q map[string]string) {
	switch q["type"] {
	case "ws":
		p["network"] = "ws"
		ws := map[string]any{}
		if path := q["path"]; path != "" {
			ws["path"] = path
		}
		if h := q["host"]; h != "" {
			ws["headers"] = map[string]any{"Host": h}
		}
		if len(ws) > 0 {
			p["ws-opts"] = ws
		}
	case "grpc":
		p["network"] = "grpc"
		if sn := q["serviceName"]; sn != "" {
			p["grpc-opts"] = map[string]any{"grpc-service-name": sn}
		}
	}
}

// ---------------------------------------------------------------------------
// vless://uuid@host:port?encryption=none&security=tls&sni=x&type=ws&path=/xx#名称
// ---------------------------------------------------------------------------

func parseVLESS(rest string) (map[string]any, error) {
	body, name := splitFragment(rest)
	authority, query, _ := strings.Cut(body, "?")
	q := parseQuery(query)
	uuid, hostport, found := strings.Cut(authority, "@")
	if !found {
		return nil, fmt.Errorf("vless 缺少 @ 分隔")
	}
	server, port, err := splitHostPort(hostport, 443)
	if err != nil {
		return nil, err
	}
	if uuid == "" || server == "" {
		return nil, fmt.Errorf("vless 缺少 uuid 或服务器地址")
	}
	p := map[string]any{
		"type":   "vless",
		"name":   name,
		"server": server,
		"port":   port,
		"uuid":   uuid,
	}
	if f := q["flow"]; f != "" {
		p["flow"] = f
	}
	switch q["security"] {
	case "tls":
		p["tls"] = true
	case "reality":
		p["tls"] = true
		ro := map[string]any{}
		if pk := q["pbk"]; pk != "" {
			ro["public-key"] = pk
		}
		if sid := q["sid"]; sid != "" {
			ro["short-id"] = sid
		}
		if len(ro) > 0 {
			p["reality-opts"] = ro
		}
	}
	if sni := q["sni"]; sni != "" {
		p["servername"] = sni
	}
	if fp := q["fp"]; fp != "" {
		p["client-fingerprint"] = fp
	}
	if v := firstOf(q, "allowInsecure", "insecure"); v == "1" || v == "true" {
		p["skip-cert-verify"] = true
	}
	tlsTransportOpts(p, q)
	return p, nil
}

// ---------------------------------------------------------------------------
// vmess://base64(JSON)（JSON 含 v/ps/add/port/id/aid/net/tls/sni/path/host）
// ---------------------------------------------------------------------------

func parseVMESS(rest string) (map[string]any, error) {
	body, name := splitFragment(rest)
	data, okb := b64Decode(body)
	if !okb {
		return nil, fmt.Errorf("vmess base64 解码失败")
	}
	var v struct {
		PS   string   `json:"ps"`
		Add  string   `json:"add"`
		Port any      `json:"port"`
		ID   string   `json:"id"`
		Aid  any      `json:"aid"`
		Scy  string   `json:"scy"`
		Net  string   `json:"net"`
		TLS  string   `json:"tls"`
		SNI  string   `json:"sni"`
		Path string   `json:"path"`
		Host string   `json:"host"`
		FP   string   `json:"fp"`
		Alpn []string `json:"alpn"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("vmess JSON 解析失败: %w", err)
	}
	port, okp := toInt(v.Port)
	if !okp || v.Add == "" || v.ID == "" {
		return nil, fmt.Errorf("vmess 缺少 add/port/id")
	}
	if name == "" {
		name = v.PS
	}
	cipher := v.Scy
	if cipher == "" {
		cipher = "auto"
	}
	aid, oka := toInt(v.Aid)
	if !oka {
		aid = 0
	}
	p := map[string]any{
		"type":    "vmess",
		"name":    name,
		"server":  v.Add,
		"port":    port,
		"uuid":    v.ID,
		"alterId": aid,
		"cipher":  cipher,
	}
	if v.TLS == "tls" || v.TLS == "true" || v.TLS == "1" {
		p["tls"] = true
	}
	if v.SNI != "" {
		p["servername"] = v.SNI
	}
	if v.FP != "" {
		p["client-fingerprint"] = v.FP
	}
	if len(v.Alpn) > 0 {
		p["alpn"] = v.Alpn
	}
	switch v.Net {
	case "ws":
		p["network"] = "ws"
		ws := map[string]any{}
		if v.Path != "" {
			ws["path"] = v.Path
		}
		if v.Host != "" {
			ws["headers"] = map[string]any{"Host": v.Host}
		}
		if len(ws) > 0 {
			p["ws-opts"] = ws
		}
	case "grpc":
		p["network"] = "grpc"
		if v.Path != "" {
			p["grpc-opts"] = map[string]any{"grpc-service-name": v.Path}
		}
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// ss://base64(method:password)@host:port#name
// ss://base64(method:password@host:port)#name
// ---------------------------------------------------------------------------

func parseSS(rest string) (map[string]any, error) {
	body, name := splitFragment(rest)
	body = strings.TrimSuffix(body, "/")

	var method, password, server string
	var port int

	if strings.Contains(body, "@") {
		// 形态一：userinfo(base64(method:password) 或明文)@host:port[?plugin]
		userinfo, hostpart, _ := strings.Cut(body, "@")
		hostpart = strings.SplitN(hostpart, "?", 2)[0]
		h, pt, err := splitHostPort(hostpart, 443)
		if err != nil {
			return nil, err
		}
		server, port = h, pt
		method, password = splitMethodPassword(userinfo)
	} else {
		// 形态二：整段为 base64(method:password@host:port)
		whole := strings.SplitN(body, "?", 2)[0]
		data, okb := b64Decode(whole)
		if !okb {
			return nil, fmt.Errorf("ss base64 解码失败")
		}
		userinfo, hostpart, found := strings.Cut(string(data), "@")
		if !found {
			return nil, fmt.Errorf("ss 内容缺少 @ 分隔")
		}
		h, pt, err := splitHostPort(hostpart, 443)
		if err != nil {
			return nil, err
		}
		server, port = h, pt
		method, password = splitMethodPassword(userinfo)
	}
	if method == "" || server == "" {
		return nil, fmt.Errorf("ss 缺少加密方式或服务器地址")
	}
	return map[string]any{
		"type":     "ss",
		"name":     name,
		"server":   server,
		"port":     port,
		"cipher":   method,
		"password": password,
	}, nil
}

// splitMethodPassword 解析 "method:password"（base64 或明文），失败返回空 method。
func splitMethodPassword(userinfo string) (string, string) {
	if dec, okb := b64Decode(userinfo); okb {
		userinfo = string(dec)
	}
	m, pw, _ := strings.Cut(userinfo, ":")
	return strings.TrimSpace(m), pw
}

// ---------------------------------------------------------------------------
// hysteria2://password@host:port?sni=x&insecure=1#name （hy2 同义）
// ---------------------------------------------------------------------------

func parseHysteria2(rest string) (map[string]any, error) {
	body, name := splitFragment(rest)
	authority, query, _ := strings.Cut(body, "?")
	q := parseQuery(query)

	var server string
	var port int
	var password string
	if strings.Contains(authority, "@") {
		userinfo, hostport, _ := strings.Cut(authority, "@")
		h, pt, err := splitHostPort(hostport, 443)
		if err != nil {
			return nil, err
		}
		server, port, password = h, pt, userinfo
	} else {
		h, pt, err := splitHostPort(authority, 443)
		if err != nil {
			return nil, err
		}
		server, port = h, pt
		password = q["auth"]
	}
	if password == "" {
		return nil, fmt.Errorf("hysteria2 缺少密码")
	}
	p := map[string]any{
		"type":     "hysteria2",
		"name":     name,
		"server":   server,
		"port":     port,
		"password": password,
	}
	if sni := q["sni"]; sni != "" {
		p["sni"] = sni
	}
	if v := firstOf(q, "insecure", "allowInsecure"); v == "1" || v == "true" {
		p["skip-cert-verify"] = true
	}
	if obfs := q["obfs"]; obfs != "" && obfs != "none" {
		p["obfs"] = obfs
		if opw := q["obfs-password"]; opw != "" {
			p["obfs-password"] = opw
		}
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// trojan://password@host:port?sni=x#name
// ---------------------------------------------------------------------------

func parseTrojan(rest string) (map[string]any, error) {
	body, name := splitFragment(rest)
	authority, query, _ := strings.Cut(body, "?")
	q := parseQuery(query)
	password, hostport, found := strings.Cut(authority, "@")
	if !found {
		return nil, fmt.Errorf("trojan 缺少 @ 分隔")
	}
	server, port, err := splitHostPort(hostport, 443)
	if err != nil {
		return nil, err
	}
	if password == "" || server == "" {
		return nil, fmt.Errorf("trojan 缺少密码或服务器地址")
	}
	p := map[string]any{
		"type":     "trojan",
		"name":     name,
		"server":   server,
		"port":     port,
		"password": password,
	}
	if sni := q["sni"]; sni != "" {
		p["sni"] = sni
	}
	if v := firstOf(q, "insecure", "allowInsecure"); v == "1" || v == "true" {
		p["skip-cert-verify"] = true
	}
	tlsTransportOpts(p, q)
	return p, nil
}
