package subscription

import (
	"testing"
	"time"
)

func TestParseUserInfo(t *testing.T) {
	info := ParseUserInfo("upload=1234; download=5678; total=100000; expire=1750000000")
	if info.Upload != 1234 || info.Download != 5678 || info.Total != 100000 {
		t.Fatalf("流量解析错误: %+v", info)
	}
	if info.Expire.Unix() != 1750000000 {
		t.Fatalf("expire 解析错误: %v", info.Expire)
	}
	// 缺项与非法值按 0。
	empty := ParseUserInfo("")
	if empty.Upload != 0 || !empty.Expire.IsZero() {
		t.Fatalf("空头解析错误: %+v", empty)
	}
	bad := ParseUserInfo("upload=abc; total=1")
	if bad.Upload != 0 || bad.Total != 1 {
		t.Fatalf("非法值处理错误: %+v", bad)
	}
}

func TestCountProxies(t *testing.T) {
	raw := []byte("proxies:\n  - {name: a, type: ss, server: s, port: 1, cipher: auto, password: p}\n  - {name: b, type: ss, server: s, port: 2, cipher: auto, password: p}\n")
	n, err := CountProxies(raw)
	if err != nil || n != 2 {
		t.Fatalf("CountProxies=%d err=%v", n, err)
	}
	if _, err := CountProxies([]byte("\tbroken: [")); err == nil {
		t.Fatal("非法 YAML 应报错")
	}
}

func TestProxyNames(t *testing.T) {
	names, err := ProxyNames([]byte("proxies:\n  - {name: '🇭🇰 HK:01', type: ss, server: s, port: 1, cipher: auto, password: p}\n"))
	if err != nil || len(names) != 1 || names[0] != "🇭🇰 HK:01" {
		t.Fatalf("ProxyNames=%v err=%v", names, err)
	}
}

func TestFileNameFromDisposition(t *testing.T) {
	cases := []struct{ in, want string }{
		{`attachment; filename*=UTF-8''%E9%A3%9E%E9%B8%9F%E4%BA%91`, "飞鸟云"},
		{`attachment; filename="bird.yaml"`, "bird.yaml"},
		{`attachment`, ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := fileNameFromDisposition(c.in); got != c.want {
			t.Errorf("fileNameFromDisposition(%q)=%q 期望 %q", c.in, got, c.want)
		}
	}
}

func TestExpireZero(t *testing.T) {
	info := ParseUserInfo("expire=0")
	if !info.Expire.IsZero() {
		t.Fatalf("expire=0 应为无期限: %v", info.Expire)
	}
	if info.Expire.After(time.Now()) != true && !info.Expire.IsZero() {
		t.Fatal("unreachable")
	}
}
