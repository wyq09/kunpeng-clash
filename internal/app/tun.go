package app

// TUN（虚拟网卡）—— 特权子进程模式（DESIGN §7b）。
//
// GUI 进程无 root 不能创建 utun。方案：内核切换为由 osascript 管理员授权启动的
// 特权子进程（--config-only 独立模式），GUI 经同一 external-controller 地址与密钥
// 继续控制（ProxyService/事件流无缝接管），系统代理与 TUN 可共存。
//
// 开启：state.tun=true → 生成 tun.enabled 的 runtime.yaml → 内嵌内核 Handoff
// （停 executor 保留事件流）→ 准备 <dataDir>/tun/（硬链接 geodata）→ 提权启动 →
// 轮询 /version（20s）→ StatusEvent(Tun=true)。
// 关闭：写 quit 标记（root 进程轮询自杀，免二次提权）→ 轮询退出（10s 超时则
// 提权 pkill 兜底）→ 清标记 → 重启内嵌内核 → StatusEvent(Tun=false)。
//
// 管理员授权被取消/超时：回滚 state.tun=false 并重启内嵌内核。

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TUN 目录与文件约定（均相对数据目录）。
const (
	tunDirName     = "tun"          // 特权内核 HomeDir（GUI 创建，用户属主）
	quitFileName   = "quit"         // 退出标记：存在即特权内核自杀
	tunRuntimeName = "runtime.yaml" // 供特权内核 REST 重载的配置镜像（IsSafePath 要求在 HomeDir 内）
)

// TUN 切换轮询参数（测试可注入短值）。
var (
	tunReadyTimeout  = 20 * time.Second // 开启后等待 /version 就绪
	tunQuitTimeout   = 10 * time.Second // 关闭时等待特权内核退出
	tunProbeInterval = 500 * time.Millisecond
)

func (a *App) tunDir() string         { return filepath.Join(a.dataDir, tunDirName) }
func (a *App) quitFilePath() string   { return filepath.Join(a.tunDir(), quitFileName) }
func (a *App) tunRuntimePath() string { return filepath.Join(a.tunDir(), tunRuntimeName) }

// isTunActive 报告特权内核当前是否接管中（受 a.mu 保护）。
func (a *App) isTunActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tunActive
}

// enableTun 开启 TUN（用户行为）：置状态 → 写配置 → 移交内核 → startTunCore。
func (a *App) enableTun() error {
	a.mu.Lock()
	if a.tunActive {
		a.mu.Unlock()
		return nil // 幂等
	}
	activeID := a.state.ActiveProfileID
	a.tunActive = true // 先置位，失败走回滚
	a.state.Tun = true
	a.mu.Unlock()

	rollback := func(reason string) error {
		reason = reason + tailCoreLog(a.tunDir())
		log.Printf("[kunpeng][tun] 开启失败，回滚: %s", reason)
		a.mu.Lock()
		a.tunActive = false
		a.state.Tun = false
		a.mu.Unlock()
		_ = a.saveState()
		// runtime.yaml 恢复为非 TUN 版本，并重启内嵌内核兜底。
		if activeID != "" {
			if b, err := a.buildRuntime(activeID); err == nil {
				_ = os.WriteFile(a.runtimePath(), b, 0o644)
			}
		}
		if serr := a.CoreStart(); serr != nil {
			log.Printf("[kunpeng][tun] 回滚重启内嵌内核失败: %v", serr)
		}
		a.pushStatusMessage(reason)
		return errors.New(reason)
	}

	if activeID == "" {
		return rollback("请先激活订阅再开启虚拟网卡")
	}
	// 1. 生成并落盘 tun.enabled 的 runtime.yaml（state.Tun 已置位）。
	bytes, err := a.buildRuntime(activeID)
	if err != nil {
		return rollback("生成 TUN 运行配置失败: " + err.Error())
	}
	if err := os.WriteFile(a.runtimePath(), bytes, 0o644); err != nil {
		return rollback("写入 runtime.yaml 失败: " + err.Error())
	}
	if err := a.saveState(); err != nil {
		log.Printf("[kunpeng][tun] 保存 state.json 失败: %v", err)
	}

	// 2. 停内嵌内核（保留事件流；系统代理保持——与 TUN 可共存）。
	a.core.Handoff()

	// 3-5. 准备目录、提权启动、等待就绪；失败回滚。
	if err := a.startTunCore(); err != nil {
		return rollback(err.Error())
	}
	log.Printf("[kunpeng][tun] 特权内核已接管（%s），TUN 已开启", a.ctlAddr)
	a.pushStatus("")
	return nil
}

// startTunCore 启动特权内核：准备 tun 目录与配置镜像 → 清 quit 标记 →
// osascript 提权启动 → 轮询 /version 就绪。失败时内嵌内核已 Handoff，
// 调用方负责回滚（rollbackTun / rollback 闭包）。
func (a *App) startTunCore() error {
	if err := a.prepareTunDir(); err != nil {
		return fmt.Errorf("准备 TUN 目录失败: %w", err)
	}
	// 配置镜像：mihomo IsSafePath 要求重载路径在特权内核 HomeDir（tun/）内。
	bytes, err := os.ReadFile(a.runtimePath())
	if err != nil {
		return fmt.Errorf("读取 runtime.yaml 失败: %w", err)
	}
	if err := os.WriteFile(a.tunRuntimePath(), bytes, 0o644); err != nil {
		return fmt.Errorf("写入 TUN 配置镜像失败: %w", err)
	}
	_ = os.Remove(a.quitFilePath()) // 清除残留退出标记
	// 等 GUI 侧 mixed-port 真正释放（Handoff 的热应用关闭是准同步的，防御性等待）：
	// 若 5s 后仍被占用，说明有第三方抢占（或 AutoConnect 竞态），提前失败避免
	// 特权核起来后立刻被端口挤死。
	port := a.MixedPort()
	relDeadline := time.Now().Add(5 * time.Second)
	for {
		if !portFree(port) { // portFree=false 表示仍被占用
			break // 已释放
		}
		if time.Now().After(relDeadline) {
			return fmt.Errorf("混合代理端口 %d 迟迟未释放（可能有其他代理软件占用），已取消开启", port)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := a.launchTunPrivileged(); err != nil {
		return fmt.Errorf("管理员授权未完成: %w", err)
	}
	if err := waitAPIReady(a.core.Ping, tunReadyTimeout, tunProbeInterval); err != nil {
		return fmt.Errorf("特权内核未在 %s 内就绪", tunReadyTimeout)
	}
	return nil
}

// disableTun 关闭 TUN：quit 标记自杀 → 重启内嵌内核。
func (a *App) disableTun() error {
	a.mu.Lock()
	wasActive := a.tunActive
	a.tunActive = false
	a.state.Tun = false
	a.mu.Unlock()
	if err := a.saveState(); err != nil {
		log.Printf("[kunpeng][tun] 保存 state.json 失败: %v", err)
	}
	if !wasActive {
		a.pushStatus("") // 幂等：刷新状态即可
		return nil
	}
	if err := a.stopTunCore(); err != nil {
		log.Printf("[kunpeng][tun] 特权内核停止异常: %v", err)
	}
	// 特权内核刚退出的瞬间端口可能尚未完全释放，短暂重试内嵌内核启动。
	var startErr error
	for i := 0; i < 5; i++ {
		if startErr = a.CoreStart(); startErr == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if startErr != nil {
		a.pushStatusMessage(startErr.Error())
		return startErr
	}
	log.Printf("[kunpeng][tun] TUN 已关闭，内嵌内核已恢复")
	a.pushStatus("")
	return nil
}

// stopTunCore 停止特权内核：写 quit 标记 → 轮询 /version 失联（root 进程自杀，
// 免二次提权）→ 超时则提权 pkill 兜底 → 清理标记文件。
func (a *App) stopTunCore() error {
	if err := os.WriteFile(a.quitFilePath(), []byte("quit"), 0o644); err != nil {
		return fmt.Errorf("写入退出标记失败: %w", err)
	}
	if err := waitAPIGone(a.core.Ping, tunQuitTimeout, tunProbeInterval); err != nil {
		log.Printf("[kunpeng][tun] 特权内核未在 %s 内退出，提权 pkill 兜底", tunQuitTimeout)
		if _, perr := osascriptAdmin(`pkill -f "config-only"`); perr != nil {
			log.Printf("[kunpeng][tun] 提权 pkill 失败（可能用户取消授权）: %v", perr)
		}
		waitAPIGone(a.core.Ping, 5*time.Second, tunProbeInterval)
	}
	_ = os.Remove(a.quitFilePath())
	return nil
}

// prepareTunDir 创建特权运行目录并把 geodata 硬链接进去（跨设备时退化为拷贝）。
// cache.db 由 root 首次运行自建（tun/cache.db，GUI 不读写），此处不管。
func (a *App) prepareTunDir() error {
	if err := os.MkdirAll(a.tunDir(), 0o755); err != nil {
		return err
	}
	for _, name := range []string{"geoip.metadb", "GEOSITE.dat"} {
		src := filepath.Join(a.dataDir, name)
		dst := filepath.Join(a.tunDir(), name)
		if _, err := os.Stat(dst); err == nil {
			continue // 已就位（上次开启留下，内容不变可直接复用）
		}
		if linkErr := os.Link(src, dst); linkErr != nil {
			data, rerr := os.ReadFile(src)
			if rerr != nil {
				return fmt.Errorf("读取 %s 失败: %v", name, rerr)
			}
			if werr := os.WriteFile(dst, data, 0o644); werr != nil {
				return fmt.Errorf("写入 %s 失败: %v", name, werr)
			}
		}
	}
	return nil
}

// launchTunPrivileged 经 osascript 管理员授权启动特权子进程：
//
//	nohup <exec> --headless --config-only --config <tunRuntime> --data-dir <tunDir>
//	       --controller <ctlAddr> --secret <secret> --quit-file <quitPath> >/dev/null 2>&1 &
//
// 用户取消授权 → 返回错误（调用方回滚）。
func (a *App) launchTunPrivileged() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件失败: %w", err)
	}
	a.mu.Lock()
	secret := a.state.Secret
	a.mu.Unlock()
	coreLog := filepath.Join(a.tunDir(), "core.log")
	_ = os.Truncate(coreLog, 0) // 每次开启截断旧日志
	shell := fmt.Sprintf("nohup %s --headless --config-only --config %s --data-dir %s --controller %s --secret %s --quit-file %s >>%s 2>&1 &",
		shq(execPath), shq(a.tunRuntimePath()), shq(a.tunDir()), shq(a.ctlAddr), shq(secret), shq(a.quitFilePath()), shq(coreLog))
	log.Printf("[kunpeng][tun] 请求管理员授权启动特权内核…")
	_, err = osascriptAdmin(shell)
	return err
}

// rollbackTun TUN 异常时的统一回滚：清状态、恢复配置、重启内嵌内核。
func (a *App) rollbackTun(reason string) {
	log.Printf("[kunpeng][tun] 回滚: %s", reason)
	a.mu.Lock()
	a.tunActive = false
	a.state.Tun = false
	activeID := a.state.ActiveProfileID
	a.mu.Unlock()
	_ = a.saveState()
	if activeID != "" {
		if b, err := a.buildRuntime(activeID); err == nil {
			_ = os.WriteFile(a.runtimePath(), b, 0o644)
		}
	}
	if serr := a.CoreStart(); serr != nil {
		log.Printf("[kunpeng][tun] 回滚重启内嵌内核失败: %v", serr)
	}
	a.pushStatusMessage(reason)
}

// portFree 探测 127.0.0.1:port 是否可连接（可连接=被占用）。
func portFree(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return true // 连不上 = 空闲
	}
	_ = conn.Close()
	return false
}

// tailCoreLog 读特权核日志尾部拼进错误信息（排障透出）。
func tailCoreLog(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "core.log"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	return "（内核日志: " + strings.Join(lines, " | ") + "）"
}

// osascriptAdmin 以管理员权限执行 shell 命令（弹出系统授权对话框）。
// 返回命令输出与错误；用户取消授权时错误非 nil。
func osascriptAdmin(shell string) (string, error) {
	script := "do shell script " + strconv.Quote(shell) + " with administrator privileges"
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// shq shell 双引号转义（路径含空格/中文安全）。
func shq(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`")
	return `"` + r.Replace(s) + `"`
}

// waitAPIReady 轮询直到 ping 成功或超时。
func waitAPIReady(ping func() error, max, interval time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		if ping() == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("等待内核就绪超时")
		}
		time.Sleep(interval)
	}
}

// waitAPIGone 轮询直到 ping 失败（内核退出）或超时。
func waitAPIGone(ping func() error, max, interval time.Duration) error {
	deadline := time.Now().Add(max)
	for {
		if ping() != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("等待内核退出超时")
		}
		time.Sleep(interval)
	}
}
