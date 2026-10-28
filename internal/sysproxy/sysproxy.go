// Package sysproxy 用 networksetup 实现 macOS 系统代理的写入/清除/检测。
//
// 契约见 DESIGN.md §8：对所有活动网络服务写 HTTP/HTTPS/SOCKS 三类代理
// 指向 127.0.0.1:<port>，并设置排除域名；清除即三类 off。
package sysproxy

import (
	"fmt"
	"os/exec"
	"strings"
)

// DefaultBypass 系统代理默认排除域名（DESIGN.md §8，参考 FlClash）。
var DefaultBypass = []string{
	"127.0.0.1", "localhost", "192.168.0.0/16", "10.0.0.0/8",
	"172.16.0.0/12", "*.local", "*.cn", "*.msftconnecttest.com", "*.msftncsi.com",
}

// run 执行 networksetup 子命令，返回合并 stderr 的错误。
func run(args ...string) (string, error) {
	out, err := exec.Command("networksetup", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("networksetup %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Services 列出网络服务（过滤掉以 * 开头的禁用项与空行）。
func Services() ([]string, error) {
	out, err := run("-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var svcs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		svcs = append(svcs, line)
	}
	if len(svcs) == 0 {
		return nil, fmt.Errorf("networksetup 未发现任何网络服务")
	}
	return svcs, nil
}

// Enable 对每个网络服务写入代理（HTTP/HTTPS/SOCKS + 排除域名）。
func Enable(port int, bypass []string) error {
	if port <= 0 {
		return fmt.Errorf("sysproxy: 端口非法: %d", port)
	}
	if len(bypass) == 0 {
		bypass = DefaultBypass
	}
	svcs, err := Services()
	if err != nil {
		return err
	}
	var firstErr error
	for _, svc := range svcs {
		steps := [][]string{
			{"-setwebproxy", svc, "127.0.0.1", fmt.Sprint(port)},
			{"-setsecurewebproxy", svc, "127.0.0.1", fmt.Sprint(port)},
			{"-setsocksfirewallproxy", svc, "127.0.0.1", fmt.Sprint(port)},
			{"-setproxybypassdomains", svc},
		}
		steps[3] = append(steps[3], bypass...)
		for _, step := range steps {
			if _, err := run(step...); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Disable 关闭所有网络服务的代理设置。
//
// 库/系统事实：`-setwebproxy <svc> off` 在本机 macOS 上会把字面量 "off"
// 写成代理主机并保持 HTTPEnable=1（流量指向死地址）；正确的关闭命令是
// `-setwebproxystate <svc> off`（同理 secure / socksfirewall）。
func Disable() error {
	svcs, err := Services()
	if err != nil {
		return err
	}
	var firstErr error
	for _, svc := range svcs {
		for _, step := range [][]string{
			{"-setwebproxystate", svc, "off"},
			{"-setsecurewebproxystate", svc, "off"},
			{"-setsocksfirewallproxystate", svc, "off"},
		} {
			if _, err := run(step...); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Enabled 报告第一个非禁用网络服务的 HTTP 代理是否开启。
// （仅作状态展示用；写入/清除由我们自己的调用跟踪。）
func Enabled() bool {
	svcs, err := Services()
	if err != nil || len(svcs) == 0 {
		return false
	}
	out, err := run("-getwebproxy", svcs[0])
	if err != nil {
		return false
	}
	return strings.Contains(out, "Enabled: Yes")
}
