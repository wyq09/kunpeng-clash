package nodelink

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// expect 断言单个 proxy 字段。
func expect(t *testing.T, p map[string]any, key string, want any) {
	t.Helper()
	got, ok := p[key]
	if !ok {
		t.Fatalf("字段 %s 缺失: %v", key, p)
	}
	gotS, wantS := fmt.Sprintf("%v", got), fmt.Sprintf("%v", want)
	if gotS != wantS {
		t.Fatalf("字段 %s = %v 期望 %v", key, got, want)
	}
}

func TestParseVLESS(t *testing.T) {
	// 真实格式：tls + ws + path + 中文名
	line := "vless://d342d11e-d424-4883-b5de-3a2c0f2d4e15@1.2.3.4:443?encryption=none&security=tls&sni=example.com&fp=chrome&type=ws&host=cdn.example.com&path=%2Fws#%F0%9F%87%AD%F0%9F%87%B0%20%E8%87%AA%E5%BB%BA%20VLESS"
	ps, ok, failed := Parse(line)
	if ok != 1 || failed != 0 {
		t.Fatalf("ok=%d failed=%d", ok, failed)
	}
	p := ps[0]
	expect(t, p, "type", "vless")
	expect(t, p, "name", "🇭🇰 自建 VLESS")
	expect(t, p, "server", "1.2.3.4")
	expect(t, p, "port", 443)
	expect(t, p, "uuid", "d342d11e-d424-4883-b5de-3a2c0f2d4e15")
	expect(t, p, "tls", true)
	expect(t, p, "servername", "example.com")
	expect(t, p, "client-fingerprint", "chrome")
	expect(t, p, "network", "ws")
	ws, _ := p["ws-opts"].(map[string]any)
	if ws["path"] != "/ws" {
		t.Fatalf("ws-opts.path = %v", ws["path"])
	}
	headers, _ := ws["headers"].(map[string]any)
	if headers["Host"] != "cdn.example.com" {
		t.Fatalf("ws-opts.headers.Host = %v", headers["Host"])
	}
}

func TestParseVLESSReality(t *testing.T) {
	line := "vless://uuid-x@abc.com:8443?security=reality&sni=www.microsoft.com&fp=chrome&pbk=pUBLICKEY123&sid=6ba85179&type=tcp&flow=xtls-rprx-vision#Reality节点"
	ps, ok, failed := Parse(line)
	if ok != 1 || failed != 0 {
		t.Fatalf("ok=%d failed=%d", ok, failed)
	}
	p := ps[0]
	expect(t, p, "tls", true)
	expect(t, p, "flow", "xtls-rprx-vision")
	ro, _ := p["reality-opts"].(map[string]any)
	if ro["public-key"] != "pUBLICKEY123" || ro["short-id"] != "6ba85179" {
		t.Fatalf("reality-opts = %v", ro)
	}
	if _, has := p["network"]; has {
		t.Fatalf("tcp 不应有 network 字段")
	}
}

func TestParseVMESS(t *testing.T) {
	payload := map[string]any{
		"v": "2", "ps": "日本 vmess", "add": "jp.example.com", "port": "8443",
		"id": "23ad6b10-8d1a-40fe-a5b3-1a4a9d3d1e3f", "aid": "0", "scy": "auto",
		"net": "ws", "tls": "tls", "sni": "jp.example.com", "host": "jp.example.com", "path": "/ray",
	}
	b, _ := json.Marshal(payload)
	// 用 RawURLEncoding（无 padding、URL-safe）模拟真实分享端常见编码
	line := "vmess://" + base64.RawURLEncoding.EncodeToString(b)
	ps, ok, failed := Parse(line)
	if ok != 1 || failed != 0 {
		t.Fatalf("ok=%d failed=%d", ok, failed)
	}
	p := ps[0]
	expect(t, p, "type", "vmess")
	expect(t, p, "name", "日本 vmess")
	expect(t, p, "server", "jp.example.com")
	expect(t, p, "port", 8443)
	expect(t, p, "uuid", "23ad6b10-8d1a-40fe-a5b3-1a4a9d3d1e3f")
	expect(t, p, "alterId", 0)
	expect(t, p, "cipher", "auto")
	expect(t, p, "tls", true)
	expect(t, p, "servername", "jp.example.com")
	ws, _ := p["ws-opts"].(map[string]any)
	if ws["path"] != "/ray" {
		t.Fatalf("ws-opts.path = %v", ws["path"])
	}
}

func TestParseSSTwoForms(t *testing.T) {
	// 形态一：base64(method:password)@host:port
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:p@ss:word"))
	line1 := "ss://" + userinfo + "@1.2.3.4:8388#%E9%A6%99%E6%B8%AF%2001"
	ps, ok, failed := Parse(line1)
	if ok != 1 || failed != 0 {
		t.Fatalf("形态一 ok=%d failed=%d", ok, failed)
	}
	p := ps[0]
	expect(t, p, "type", "ss")
	expect(t, p, "name", "香港 01")
	expect(t, p, "cipher", "aes-128-gcm")
	expect(t, p, "password", "p@ss:word")
	expect(t, p, "server", "1.2.3.4")
	expect(t, p, "port", 8388)

	// 形态二：base64(method:password@host:port)（无 padding url-safe 也要能解）
	whole := base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:secret@5.6.7.8:443"))
	line2 := "ss://" + whole + "#SS02"
	ps2, ok2, failed2 := Parse(line2)
	if ok2 != 1 || failed2 != 0 {
		t.Fatalf("形态二 ok=%d failed=%d", ok2, failed2)
	}
	q := ps2[0]
	expect(t, q, "cipher", "chacha20-ietf-poly1305")
	expect(t, q, "password", "secret")
	expect(t, q, "server", "5.6.7.8")
	expect(t, q, "port", 443)
	expect(t, q, "name", "SS02")

	// 形态三：明文 userinfo（部分客户端不编码）
	line3 := "ss://aes-256-gcm:plainpass@9.9.9.9:1234#SS03"
	ps3, ok3, _ := Parse(line3)
	if ok3 != 1 {
		t.Fatalf("形态三 ok=%d", ok3)
	}
	expect(t, ps3[0], "cipher", "aes-256-gcm")
	expect(t, ps3[0], "password", "plainpass")
}

func TestParseHysteria2(t *testing.T) {
	line := "hysteria2://letmein@example.com:8443?sni=example.com&insecure=1&obfs=salamander&obfs-password=obfspw#HY2%E8%87%AA%E5%BB%BA"
	ps, ok, failed := Parse(line)
	if ok != 1 || failed != 0 {
		t.Fatalf("ok=%d failed=%d", ok, failed)
	}
	p := ps[0]
	expect(t, p, "type", "hysteria2")
	expect(t, p, "name", "HY2自建")
	expect(t, p, "server", "example.com")
	expect(t, p, "port", 8443)
	expect(t, p, "password", "letmein")
	expect(t, p, "sni", "example.com")
	expect(t, p, "skip-cert-verify", true)
	expect(t, p, "obfs", "salamander")
	expect(t, p, "obfs-password", "obfspw")

	// hy2 别名 + auth 参数形态
	line2 := "hy2://example.net:36712/?auth=secretauth#hy2auth"
	ps2, ok2, _ := Parse(line2)
	if ok2 != 1 {
		t.Fatalf("hy2 ok=%d", ok2)
	}
	expect(t, ps2[0], "password", "secretauth")
	expect(t, ps2[0], "port", 36712)
}

func TestParseTrojan(t *testing.T) {
	line := "trojan://pwd123@trojan.example.com:443?sni=example.com&allowInsecure=0#%E7%89%B9%E6%B4%9B%E4%BC%8A"
	ps, ok, failed := Parse(line)
	if ok != 1 || failed != 0 {
		t.Fatalf("ok=%d failed=%d", ok, failed)
	}
	p := ps[0]
	expect(t, p, "type", "trojan")
	expect(t, p, "name", "特洛伊")
	expect(t, p, "server", "trojan.example.com")
	expect(t, p, "port", 443)
	expect(t, p, "password", "pwd123")
	expect(t, p, "sni", "example.com")
	if _, has := p["skip-cert-verify"]; has {
		t.Fatalf("allowInsecure=0 不应有 skip-cert-verify")
	}
}

func TestParseBatchAndAutoName(t *testing.T) {
	raw := strings.Join([]string{
		"hysteria2://pw1@a.com:443?sni=a.com#A节点",
		"", // 空行忽略
		"trojan://pw2@b.com:443#B节点",
		"ss://YmYtY2KtbG9jYWw6cGFzc3dvcmQ=@c.com:8388", // 无名 → 自动命名
		"# 纯注释行忽略",
		"https://not-a-node.example/sub", // 非法行 → failed
		"garbage-line",                   // 非法行 → failed
		"ftp://x.com",                    // 不支持协议 → failed
	}, "\n")
	ps, ok, failed := Parse(raw)
	if ok != 3 || failed != 3 {
		t.Fatalf("ok=%d failed=%d（期望 3/3）", ok, failed)
	}
	if ps[2]["name"] != "自定义节点1" {
		t.Fatalf("自动命名异常: %v", ps[2]["name"])
	}
	// 重复名在同一批内保留（去重由 app 层负责）
	ps2, ok2, _ := Parse("ss://YWVzLTEyOC1nY206cA==@a.com:1#重名\nss://YWVzLTEyOC1nY206cA==@b.com:1#重名")
	if ok2 != 2 || ps2[0]["name"] != "重名" || ps2[1]["name"] != "重名" {
		t.Fatalf("重名处理异常: ok=%d", ok2)
	}
}

func TestParseRejectsBrokenLinks(t *testing.T) {
	cases := []string{
		"vless://no-at-host.com:443#x", // 缺 @
		"vmess://not_base64!!",         // base64 坏
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"ps":"x"}`)), // 缺 add/id
		"ss://YWVzLTEyOC1nY206cA==@1.2.3.4:badport#x",                        // 端口坏
		"ss://@1.2.3.4:1#x",          // 空 userinfo（缺加密方式）
		"trojan://@host.com:443#x",   // 缺密码
		"hysteria2://host.com:443#x", // 缺密码（无 auth/userinfo）
	}
	for _, c := range cases {
		_, ok, failed := Parse(c)
		if ok != 0 || failed != 1 {
			t.Fatalf("%q 应解析失败（ok=%d failed=%d）", c, ok, failed)
		}
	}
}
