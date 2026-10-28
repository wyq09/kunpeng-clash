package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"kunpeng/internal/configgen"
	"kunpeng/internal/corectl"
	"kunpeng/internal/nodelink"
	"kunpeng/internal/subscription"
	"kunpeng/internal/sysproxy"
)

// 外部服务实现（main.go 契约）。

// ProfilesList 返回全部订阅（激活的排最前，其余按名称）。
func (a *App) ProfilesList() []Profile {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Profile, 0, len(a.profiles))
	for _, p := range a.profiles {
		out = append(out, p.toProfile(a.state.ActiveProfileID))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ProfileAdd 拉取 url 并新增订阅（name 为空时取订阅文件名）。
func (a *App) ProfileAdd(rawURL, name string) (Profile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res, err := subscription.Fetch(ctx, rawURL)
	if err != nil {
		return Profile{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// 同 URL 去重：直接覆盖已有订阅内容（幂等导入）。
	for i := range a.profiles {
		if a.profiles[i].URL == rawURL {
			if err := a.updateProfileLocked(&a.profiles[i], res); err != nil {
				return Profile{}, err
			}
			return a.profiles[i].toProfile(a.state.ActiveProfileID), nil
		}
	}
	rec := profileRecord{
		ID:        newID(),
		URL:       rawURL,
		UpdatedAt: nowRFC3339(),
	}
	if name != "" {
		rec.Name = name
	} else if res.Name != "" {
		rec.Name = res.Name
	} else {
		rec.Name = "订阅 " + time.Now().Format("01-02 15:04")
	}
	rec.NodeCount = res.NodeCount
	rec.Upload, rec.Download, rec.Total = res.UserInfo.Upload, res.UserInfo.Download, res.UserInfo.Total
	rec.Expire = expireRFC3339(res.UserInfo.Expire)
	if err := a.writeProfileRawLocked(rec.ID, res.Raw); err != nil {
		return Profile{}, err
	}
	a.profiles = append(a.profiles, rec)
	if err := a.saveProfilesLocked(); err != nil {
		return Profile{}, err
	}
	return rec.toProfile(a.state.ActiveProfileID), nil
}

// ProfileUpdate 重新拉取订阅内容与流量信息；激活订阅且内核可用时热应用
// （含 TUN 特权内核 REST 重载）；调度器与手动更新共用本方法。
func (a *App) ProfileUpdate(id string) (Profile, error) {
	subscription.SetProxyFallback(fmt.Sprintf("127.0.0.1:%d", a.MixedPort()))
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	a.mu.Lock()
	rec := a.findLocked(id)
	url := ""
	wasActive := rec != nil && a.state.ActiveProfileID == id
	if rec != nil {
		url = rec.URL
	}
	a.mu.Unlock()
	if rec == nil {
		return Profile{}, fmt.Errorf("订阅不存在: %s", id)
	}
	res, err := subscription.Fetch(ctx, url)
	if err != nil {
		return Profile{}, err
	}
	a.mu.Lock()
	rec = a.findLocked(id)
	if rec == nil {
		a.mu.Unlock()
		return Profile{}, fmt.Errorf("订阅不存在: %s", id)
	}
	if err := a.updateProfileLocked(rec, res); err != nil {
		a.mu.Unlock()
		return Profile{}, err
	}
	updated := rec.toProfile(a.state.ActiveProfileID)
	a.mu.Unlock()

	// 激活订阅 + 内核可用 → 用新内容热应用。
	if wasActive && a.coreAvailable() {
		if err := a.hotApply(); err != nil {
			return updated, fmt.Errorf("订阅已更新，但热应用失败: %w", err)
		}
	}
	// updatedAt 已刷新：重算自动更新调度（清除失败顺延标记）。
	a.rescheduleProfile(id)
	return updated, nil
}

// ProfileRemove 删除订阅；若删除的是激活订阅则先停内核并清系统代理。
func (a *App) ProfileRemove(id string) error {
	a.mu.Lock()
	idx := -1
	for i := range a.profiles {
		if a.profiles[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("订阅不存在: %s", id)
	}
	wasActive := a.state.ActiveProfileID == id
	a.profiles = append(a.profiles[:idx], a.profiles[idx+1:]...)
	if wasActive {
		a.state.ActiveProfileID = ""
	}
	a.mu.Unlock()
	_ = os.Remove(a.profileRawPath(id))
	if wasActive {
		_ = a.CoreStop()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveProfilesLocked()
}

// ProfileActivate 激活订阅：生成内核配置并热应用（未运行则启动）。
func (a *App) ProfileActivate(id string) error {
	a.mu.Lock()
	rec := a.findLocked(id)
	a.mu.Unlock()
	if rec == nil {
		return fmt.Errorf("订阅不存在: %s", id)
	}
	bytes, err := a.buildRuntime(id)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.runtimePath(), bytes, 0o644); err != nil {
		return fmt.Errorf("写入 runtime.yaml 失败: %w", err)
	}
	a.mu.Lock()
	a.state.ActiveProfileID = id
	a.mu.Unlock()
	if err := a.saveState(); err != nil {
		log.Printf("[kunpeng] 保存 state.json 失败: %v", err)
	}

	if err := a.applyRuntime(bytes); err != nil {
		a.pushStatusMessage(err.Error())
		return err
	}
	a.pushStatus("")

	// 预热：立即触发一轮自动组测速，让 url-test 马上收敛到真实可用节点，
	// 而不是等 interval（300s）或停在测速尚未完成的默认节点上。
	if a.coreAvailable() {
		go func() {
			if _, err := a.core.GroupDelay(configgen.GroupAuto, configgen.HealthURL, 5000); err != nil {
				log.Printf("[kunpeng] 自动组预热测速失败（不影响使用）: %v", err)
			}
		}()
	}
	return nil
}

// ProfileCurrent 返回当前激活订阅。
func (a *App) ProfileCurrent() (Profile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec := a.findLocked(a.state.ActiveProfileID)
	if rec == nil {
		return Profile{}, fmt.Errorf("当前没有激活的订阅")
	}
	return rec.toProfile(a.state.ActiveProfileID), nil
}

// ---------------------------------------------------------------------------
// 内核生命周期
// ---------------------------------------------------------------------------

// CoreStatus 返回当前内核与系统状态。
func (a *App) CoreStatusNow() CoreStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentStatusLocked()
}

// CoreStart 启动内核（使用当前激活订阅的配置）；已运行则幂等成功。
// TUN 模式下内核由特权子进程承载，本方法幂等空转（配置变更走 hotApply REST 重载）。
func (a *App) CoreStart() error {
	a.mu.Lock()
	activeID := a.state.ActiveProfileID
	tun := a.tunActive
	a.mu.Unlock()
	if activeID == "" {
		return errors.New("请先激活一个订阅再启动内核")
	}
	if tun || a.core.Running() {
		return nil
	}
	bytes, err := a.buildRuntime(activeID)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.runtimePath(), bytes, 0o644); err != nil {
		return fmt.Errorf("写入 runtime.yaml 失败: %w", err)
	}
	if err := a.core.Start(bytes); err != nil {
		a.pushStatusMessage(err.Error())
		return err
	}
	a.pushStatus("")
	return nil
}

// CoreStop 停止内核并无条件清系统代理（--no-sysproxy 会话除外：从未写入则不动系统设置）。
// TUN 模式下等价于关闭 TUN（特权内核与 TUN 同生命周期），不重启内嵌内核。
func (a *App) CoreStop() error {
	if a.isTunActive() {
		a.mu.Lock()
		a.state.Tun = false
		a.mu.Unlock()
		if err := a.saveState(); err != nil {
			log.Printf("[kunpeng] 保存 state.json 失败: %v", err)
		}
		if err := a.stopTunCore(); err != nil {
			log.Printf("[kunpeng][tun] 特权内核停止异常: %v", err)
		}
		a.clearSystemProxyIfOurs()
		a.pushStatus("")
		return nil
	}
	if !a.core.Running() {
		// 即便状态机认为未运行（看门狗重启中），也尝试清理系统代理。
		a.clearSystemProxyIfOurs()
		a.pushStatus("")
		return nil
	}
	if err := a.core.Stop(); err != nil {
		log.Printf("[kunpeng] 内核停止异常: %v", err)
	}
	a.clearSystemProxyIfOurs()
	a.pushStatus("")
	return nil
}

// CoreRestart 重启内核（系统代理保持原开关状态）。
// TUN 模式下重启特权内核（需要重新进行一次管理员授权）。
func (a *App) CoreRestart() error {
	a.mu.Lock()
	wasSys := a.wasSys
	tun := a.tunActive
	a.mu.Unlock()
	if tun {
		if err := a.stopTunCore(); err != nil {
			log.Printf("[kunpeng][tun] 重启前停止特权内核异常: %v", err)
		}
		if err := a.startTunCore(); err != nil {
			// 重启特权内核失败：回滚到内嵌内核，TUN 关闭。
			a.rollbackTun("重启虚拟网卡失败，已回退到普通内核: " + err.Error())
			return err
		}
		a.pushStatus("")
		return nil
	}
	if a.core.Running() {
		if err := a.core.Stop(); err != nil {
			log.Printf("[kunpeng] 重启时停止异常: %v", err)
		}
		a.clearSystemProxyBestEffort()
	}
	if err := a.CoreStart(); err != nil {
		return err
	}
	if wasSys {
		if err := a.enableSystemProxy(); err != nil {
			log.Printf("[kunpeng] 重启后恢复系统代理失败: %v", err)
		}
	}
	return nil
}

// CoreSetMode 切换出站模式 rule/global/direct（持久化 + 热应用）。
func (a *App) CoreSetMode(mode string) error {
	switch mode {
	case "rule", "global", "direct":
	default:
		return fmt.Errorf("非法模式: %q（应为 rule/global/direct）", mode)
	}
	a.mu.Lock()
	a.state.Mode = mode
	a.mu.Unlock()
	if err := a.hotApply(); err != nil {
		return err
	}
	a.pushStatus("")
	return a.saveState()
}

// CoreSetSystemProxy 开关系统代理。
func (a *App) CoreSetSystemProxy(on bool) error {
	if a.opt.NoSysProxy {
		return errors.New("本次以 --no-sysproxy 启动，系统代理功能已禁用")
	}
	if on {
		if !a.coreAvailable() {
			return errors.New("内核未运行，不能开启系统代理")
		}
		if err := a.enableSystemProxy(); err != nil {
			return err
		}
	} else {
		a.clearSystemProxyBestEffort()
	}
	a.mu.Lock()
	a.state.SystemProxy = on
	a.mu.Unlock()
	a.pushStatus("")
	return a.saveState()
}

// CoreSetPort 设置混合代理端口（持久化 + 热应用 + 系统代理跟随）。
func (a *App) CoreSetPort(port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("端口需在 1024-65535 之间: %d", port)
	}
	a.mu.Lock()
	old := a.state.MixedPort
	a.state.MixedPort = port
	sysOn := a.state.SystemProxy
	a.mu.Unlock()
	if sysOn && port != old {
		// 热应用会短暂断流，直接按新端口重写系统代理。
		a.clearSystemProxyBestEffort()
	}
	if err := a.hotApply(); err != nil {
		a.mu.Lock()
		a.state.MixedPort = old
		a.mu.Unlock()
		return err
	}
	if sysOn && port != old {
		if err := a.enableSystemProxy(); err != nil {
			log.Printf("[kunpeng] 系统代理切换到新端口失败: %v", err)
		}
	}
	a.pushStatus("")
	return a.saveState()
}

// CoreSetAutoConnect 设置启动应用时自动连接。
func (a *App) CoreSetAutoConnect(on bool) error {
	a.mu.Lock()
	a.state.AutoConnect = on
	a.mu.Unlock()
	a.pushStatus("")
	return a.saveState()
}

// coreAvailable 内核当前是否可服务（内嵌内核运行中，或 TUN 特权内核接管中）。
// TUN 模式下特权内核暴露同一 external-controller 地址与密钥，REST 桥接照常可用。
func (a *App) coreAvailable() bool {
	if a.core.Running() {
		return true
	}
	return a.isTunActive()
}

// applyRuntime 把已生成的配置字节应用到当前内核：
// TUN 模式 → 镜像到 tun/ 目录后经 REST PUT /configs 让特权内核重载；
// 内嵌模式 → 内核运行中则 ApplyConfig，未运行则跳过（等下次 CoreStart）。
func (a *App) applyRuntime(bytes []byte) error {
	if a.isTunActive() {
		if err := os.WriteFile(a.tunRuntimePath(), bytes, 0o644); err != nil {
			return fmt.Errorf("写入 TUN 运行配置失败: %w", err)
		}
		return a.core.ReloadFromFile(a.tunRuntimePath())
	}
	if !a.core.Running() {
		return nil
	}
	if err := a.core.Apply(bytes); err != nil {
		a.pushStatusMessage(err.Error())
		return err
	}
	return nil
}

// hotApply 重新生成 runtime.yaml 并热应用（内嵌 / TUN 特权内核通用）。
func (a *App) hotApply() error {
	a.mu.Lock()
	activeID := a.state.ActiveProfileID
	a.mu.Unlock()
	if activeID == "" {
		return nil
	}
	bytes, err := a.buildRuntime(activeID)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.runtimePath(), bytes, 0o644); err != nil {
		return fmt.Errorf("写入 runtime.yaml 失败: %w", err)
	}
	return a.applyRuntime(bytes)
}

// ---------------------------------------------------------------------------
// ProxyService 桥接（TUN 模式下同地址特权内核直接应答）
// ---------------------------------------------------------------------------

// ProxiesAll GET /proxies 全量。
func (a *App) ProxiesAll() (map[string]any, error) {
	if !a.coreAvailable() {
		return nil, errCoreStopped
	}
	return a.core.All()
}

// ProxySelect 在组 group 中选择节点 name。
func (a *App) ProxySelect(group, name string) error {
	if !a.coreAvailable() {
		return errCoreStopped
	}
	return a.core.Select(group, name)
}

// ProxyGroupDelay 对组 group 做批量延迟测试。
func (a *App) ProxyGroupDelay(group, probeURL string, timeoutMs int) (map[string]any, error) {
	if !a.coreAvailable() {
		return nil, errCoreStopped
	}
	if probeURL == "" {
		probeURL = configgen.HealthURL
	}
	return a.core.GroupDelay(group, probeURL, timeoutMs)
}

// ProxyDelay 对单个节点做延迟测试。
func (a *App) ProxyDelay(name, probeURL string, timeoutMs int) (DelayResult, error) {
	if !a.coreAvailable() {
		return DelayResult{}, errCoreStopped
	}
	if probeURL == "" {
		probeURL = configgen.HealthURL
	}
	d, err := a.core.ProxyDelay(name, probeURL, timeoutMs)
	return DelayResult{Name: d.Name, Delay: d.Delay, Message: d.Message}, err
}

// Rules GET /rules。
func (a *App) Rules() (map[string]any, error) {
	if !a.coreAvailable() {
		return nil, errCoreStopped
	}
	return a.core.Rules()
}

// ConnectionDelete 关闭单个连接（id 来自连接快照）。
func (a *App) ConnectionDelete(id string) error {
	if !a.coreAvailable() {
		return errCoreStopped
	}
	return a.core.DeleteConnection(id)
}

// ConnectionsCloseAll 关闭全部连接。
func (a *App) ConnectionsCloseAll() error {
	if !a.coreAvailable() {
		return errCoreStopped
	}
	return a.core.CloseAllConnections()
}

// PublicIP 通过当前代理端口查询公网出口 IP。
func (a *App) PublicIP() (string, error) {
	port := a.MixedPort()
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	client := &http.Client{
		Timeout:   6 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ---------------------------------------------------------------------------
// AppService
// ---------------------------------------------------------------------------

// TestURL 通过当前代理端口请求 url，返回 HTTP 状态码（0 表示请求失败）。
func (a *App) TestURL(rawURL string) (int, error) {
	port := a.MixedPort()
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, nil
}

// Logs 返回最近的内核日志（最多 500 条）。
func (a *App) Logs() []LogEntry {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	out := make([]LogEntry, len(a.logs))
	copy(out, a.logs)
	return out
}

// OpenDataDir 在 Finder 中打开应用数据目录。
func (a *App) OpenDataDir() error {
	return exec.Command("open", a.dataDir).Start()
}

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

// buildRuntime 用激活订阅原文 + 自定义节点 + 白名单生成 runtime.yaml 字节流。
func (a *App) buildRuntime(activeID string) ([]byte, error) {
	raw, err := os.ReadFile(a.profileRawPath(activeID))
	if err != nil {
		return nil, fmt.Errorf("读取订阅原文失败: %w", err)
	}
	a.mu.Lock()
	opt := configgen.Options{
		MixedPort:          a.state.MixedPort,
		Mode:               a.state.Mode,
		ExternalController: a.ctlAddr,
		Secret:             a.state.Secret,
		Whitelist:          a.state.Whitelist,
		CustomProxies:      a.state.CustomProxies,
	}
	a.mu.Unlock()
	return configgen.Generate(raw, opt)
}

// appendLog 写环形日志并回调。
func (a *App) appendLog(typ, payload string) {
	e := LogEntry{Type: typ, Payload: payload}
	a.logMu.Lock()
	a.logs = append(a.logs, e)
	if len(a.logs) > 500 {
		a.logs = a.logs[len(a.logs)-500:]
	}
	a.logMu.Unlock()
	if a.opt.EmitLog != nil {
		a.opt.EmitLog(e)
	}
}

// pushStatus 组装并推送 CoreStatus。
func (a *App) pushStatus(message string) {
	a.mu.Lock()
	if message != "" {
		a.state.Message = message
	}
	st := a.currentStatusLocked()
	a.mu.Unlock()
	if a.opt.EmitStatus != nil {
		a.opt.EmitStatus(st)
	}
}

// pushStatusMessage 推送带信息的 StatusEvent。
func (a *App) pushStatusMessage(msg string) { a.pushStatus(msg) }

// enableSystemProxy 写系统代理（含 wasSys 记忆）。
func (a *App) enableSystemProxy() error {
	port := a.MixedPort()
	bypass := configgen.DefaultBypassDomains
	if err := sysproxy.Enable(port, bypass); err != nil {
		a.mu.Lock()
		a.wasSys = false
		a.mu.Unlock()
		return fmt.Errorf("写入系统代理失败: %w", err)
	}
	a.mu.Lock()
	a.wasSys = true
	a.mu.Unlock()
	log.Printf("[kunpeng] 系统代理已开启 -> 127.0.0.1:%d", port)
	return nil
}

// clearSystemProxyBestEffort 清系统代理（尽力而为，记录日志）。
func (a *App) clearSystemProxyBestEffort() {
	a.mu.Lock()
	a.wasSys = false
	a.mu.Unlock()
	if err := sysproxy.Disable(); err != nil {
		log.Printf("[kunpeng] 清除系统代理失败: %v", err)
		return
	}
	log.Printf("[kunpeng] 系统代理已关闭")
}

// clearSystemProxyIfOurs 只在本会话写过系统代理时清理：
// --no-sysproxy 模式从未动过系统设置，退出时不许误伤用户环境。
func (a *App) clearSystemProxyIfOurs() {
	if a.opt.NoSysProxy {
		return
	}
	a.mu.Lock()
	wrote := a.wasSys || a.state.SystemProxy
	a.mu.Unlock()
	if wrote {
		a.clearSystemProxyBestEffort()
	}
}

// currentStatusLocked 组装当前状态（调用方持 a.mu）。
// Running 在 TUN 模式下表示「特权内核可用」；Tun 表示特权子进程模式已接管。
func (a *App) currentStatusLocked() CoreStatus {
	return CoreStatus{
		Running:     a.core.Running() || a.tunActive,
		Version:     corectl.MihomoVersion,
		Mode:        a.state.Mode,
		MixedPort:   a.state.MixedPort,
		SystemProxy: a.state.SystemProxy,
		Tun:         a.tunActive,
		AutoConnect: a.state.AutoConnect,
		ProfileID:   a.state.ActiveProfileID,
		Message:     a.state.Message,
	}
}

// ---------------------------------------------------------------------------
// ProfileEdit / NodesService / Whitelist —— 正式实现
// ---------------------------------------------------------------------------

// ProfileEdit 修改订阅的名称、URL 与自动更新设置；url 变更后立即重新拉取原文与
// 流量信息；若该订阅是激活订阅且内核可用，重新生成并热应用 runtime.yaml。
// 自动更新间隔规范化：<=0 视为默认 1440 分钟，<30 收敛到下限 30 分钟。
// 保存成功后重算该订阅的自动更新调度。
func (a *App) ProfileEdit(id, name, newURL string, autoUpdate bool, updateIntervalMin int) (Profile, error) {
	interval := normalizeUpdateInterval(updateIntervalMin)
	a.mu.Lock()
	rec := a.findLocked(id)
	if rec == nil {
		a.mu.Unlock()
		return Profile{}, fmt.Errorf("订阅不存在: %s", id)
	}
	oldURL := rec.URL
	wasActive := a.state.ActiveProfileID == id
	a.mu.Unlock()

	// URL 变更：先拉取（不持锁，网络 IO 最长 45s）再落盘。
	var res *subscription.Result
	if newURL != "" && newURL != oldURL {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var err error
		res, err = subscription.Fetch(ctx, newURL)
		if err != nil {
			return Profile{}, fmt.Errorf("新订阅地址拉取失败: %w", err)
		}
	}

	a.mu.Lock()
	rec = a.findLocked(id)
	if rec == nil {
		a.mu.Unlock()
		return Profile{}, fmt.Errorf("订阅不存在: %s", id)
	}
	if name != "" {
		rec.Name = name
	}
	rec.AutoUpdate = autoUpdate
	rec.UpdateIntervalMin = interval
	if res != nil {
		rec.URL = newURL
		if err := a.updateProfileLocked(rec, res); err != nil {
			a.mu.Unlock()
			return Profile{}, err
		}
	} else if err := a.saveProfilesLocked(); err != nil {
		a.mu.Unlock()
		return Profile{}, err
	}
	updated := rec.toProfile(a.state.ActiveProfileID)
	a.mu.Unlock()

	// 激活订阅 + 内核可用 → 用新内容热应用。
	if wasActive && res != nil && a.coreAvailable() {
		if err := a.hotApply(); err != nil {
			return updated, fmt.Errorf("订阅已更新，但热应用失败: %w", err)
		}
	}
	// 自动更新设置已变更：立即重算该订阅的调度（清除失败顺延标记）。
	a.rescheduleProfile(id)
	a.pushStatus("")
	return updated, nil
}

// NodesAddFromLinks 解析多行分享链接，追加为自定义节点（重名跳过），
// 保存 state.json 并在内核运行时热应用。返回成功新增的条数。
func (a *App) NodesAddFromLinks(raw string) (int, error) {
	proxies, ok, failed := nodelink.Parse(raw)
	if ok == 0 {
		if failed > 0 {
			return 0, fmt.Errorf("全部 %d 行均无法解析（请检查链接格式）", failed)
		}
		return 0, fmt.Errorf("没有可导入的节点链接")
	}
	a.mu.Lock()
	existing := map[string]bool{}
	for _, p := range a.state.CustomProxies {
		if n, _ := p["name"].(string); n != "" {
			existing[n] = true
		}
	}
	added := 0
	for _, p := range proxies {
		n, _ := p["name"].(string)
		if n == "" || existing[n] {
			continue // 重名跳过（含同一批内的重复）
		}
		existing[n] = true
		a.state.CustomProxies = append(a.state.CustomProxies, p)
		added++
	}
	a.mu.Unlock()
	if added == 0 {
		return 0, fmt.Errorf("解析到 %d 个节点，但全部与已有节点重名", ok)
	}
	if err := a.saveState(); err != nil {
		return added, fmt.Errorf("保存 state.json 失败: %w", err)
	}
	if err := a.hotApply(); err != nil {
		return added, fmt.Errorf("节点已保存，但热应用失败: %w", err)
	}
	a.pushStatus("")
	return added, nil
}

// NodesList 返回全部自定义节点名称。
func (a *App) NodesList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.state.CustomProxies))
	for _, p := range a.state.CustomProxies {
		if n, _ := p["name"].(string); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// NodesRemove 按名称删除自定义节点，保存并在内核运行时热应用。
func (a *App) NodesRemove(name string) error {
	a.mu.Lock()
	idx := -1
	for i, p := range a.state.CustomProxies {
		if n, _ := p["name"].(string); n == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return fmt.Errorf("自定义节点不存在: %s", name)
	}
	a.state.CustomProxies = append(a.state.CustomProxies[:idx], a.state.CustomProxies[idx+1:]...)
	a.mu.Unlock()
	if err := a.saveState(); err != nil {
		return fmt.Errorf("保存 state.json 失败: %w", err)
	}
	if err := a.hotApply(); err != nil {
		return fmt.Errorf("节点已删除，但热应用失败: %w", err)
	}
	a.pushStatus("")
	return nil
}

// WhitelistGet 返回当前直连白名单。
func (a *App) WhitelistGet() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.state.Whitelist))
	copy(out, a.state.Whitelist)
	return out
}

// WhitelistSet 全量设置直连白名单：规范化（去空/去重/小写，上限 200 条）→
// 持久化 → 重新生成 runtime.yaml → 内核运行时热应用 → 推送状态。
func (a *App) WhitelistSet(domains []string) error {
	norm, err := configgen.NormalizeWhitelist(domains)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.state.Whitelist = norm
	a.mu.Unlock()
	if err := a.saveState(); err != nil {
		return fmt.Errorf("保存 state.json 失败: %w", err)
	}
	if err := a.hotApply(); err != nil {
		return fmt.Errorf("白名单已保存，但热应用失败: %w", err)
	}
	a.pushStatus("")
	return nil
}

// CoreSetTun 开关虚拟网卡（实现见 tun.go：特权子进程模式）。
func (a *App) CoreSetTun(on bool) error {
	if on {
		return a.enableTun()
	}
	return a.disableTun()
}
