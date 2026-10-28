package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kunpeng/internal/configgen"
	"kunpeng/internal/corectl"
	"kunpeng/internal/subscription"

	"go.yaml.in/yaml/v3"
)

// corectlControllerAddr external-controller 固定监听地址（DESIGN §6）。
const corectlControllerAddr = "127.0.0.1:9097"

// defaultDataDir 默认数据目录。
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "kunpeng-data"
	}
	return filepath.Join(home, "Library", "Application Support", "KunpengClash")
}

// appState state.json 的结构（内核状态/模式/端口/系统代理开关记忆）。
type appState struct {
	Mode            string                 `json:"mode"`
	MixedPort       int                    `json:"mixedPort"`
	SystemProxy     bool                   `json:"systemProxy"`
	AutoConnect     bool                   `json:"autoConnect"`
	ActiveProfileID string                 `json:"activeProfileId"`
	Secret          string                 `json:"secret"`
	Tun             bool                   `json:"tun"`                     // 虚拟网卡（特权子进程模式）
	Whitelist       []string               `json:"whitelist,omitempty"`     // 直连白名单（域名/通配/IP 段）
	CustomProxies   []map[string]any       `json:"customProxies,omitempty"` // 自定义节点（分享链接导入）
	RouteRules      map[string][]RouteRule `json:"routeRules,omitempty"`    // 分流规则，按订阅 ID 挂载（跟着配置走）
	Message         string                 `json:"-"`                       // 最近一次错误/提示（会话内，不持久化）
}

// profileRecord profiles.yaml 中的一条订阅元数据。
type profileRecord struct {
	ID                string `yaml:"id" json:"id"`
	Name              string `yaml:"name" json:"name"`
	URL               string `yaml:"url" json:"url"`
	Upload            uint64 `yaml:"upload" json:"upload"`
	Download          uint64 `yaml:"download" json:"download"`
	Total             uint64 `yaml:"total" json:"total"`
	Expire            string `yaml:"expire" json:"expire"`
	UpdatedAt         string `yaml:"updatedAt" json:"updatedAt"`
	NodeCount         int    `yaml:"nodeCount" json:"nodeCount"`
	AutoUpdate        bool   `yaml:"autoUpdate"`        // 自动更新开关（旧文件缺省 false）
	UpdateIntervalMin int    `yaml:"updateIntervalMin"` // 自动更新间隔分钟（旧文件缺省 → 默认 1440）
}

// toProfile 转换为 IPC 载荷（active 由 state 决定；interval 规范化输出）。
func (r profileRecord) toProfile(activeID string) Profile {
	return Profile{
		ID:                r.ID,
		Name:              r.Name,
		URL:               r.URL,
		Active:            r.ID == activeID && activeID != "",
		UploadBytes:       r.Upload,
		DownloadBytes:     r.Download,
		TotalBytes:        r.Total,
		Expire:            r.Expire,
		UpdatedAt:         r.UpdatedAt,
		NodeCount:         r.NodeCount,
		AutoUpdate:        r.AutoUpdate,
		UpdateIntervalMin: normalizeUpdateInterval(r.UpdateIntervalMin),
	}
}

// New 组装应用：建目录、释放 geodata、载入状态、首启动导入预置订阅。
func New(opt Options) (*App, error) {
	if opt.DataDir == "" {
		opt.DataDir = defaultDataDir()
	}
	if opt.ControllerAddr == "" {
		opt.ControllerAddr = corectlControllerAddr
	}
	a := &App{opt: opt, dataDir: opt.DataDir, ctlAddr: opt.ControllerAddr}
	// 订阅拉取的经代理兜底地址：内核运行时直连失败可走内核出网（端口后续可变，拉取时读取实时值）
	subscription.SetProxyFallback("")
	if err := os.MkdirAll(filepath.Join(a.dataDir, "profiles"), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	a.releaseGeodata()
	if err := a.loadState(); err != nil {
		return nil, err
	}
	if opt.Port > 0 {
		// --port 会话级覆盖：不持久化，仅供本次运行。
		a.state.MixedPort = opt.Port
	}
	if err := a.loadProfiles(); err != nil {
		return nil, err
	}

	ctl, err := corectl.New(corectl.Options{
		DataDir:        a.dataDir,
		ControllerAddr: a.ctlAddr,
		Secret:         a.state.Secret,
		OnLog:          a.appendLog,
		OnTraffic: func(up, down int64) {
			if opt.EmitTraffic != nil {
				opt.EmitTraffic(Traffic{Up: up, Down: down})
			}
		},
		OnMemory: func(inuse, osLimit uint64) {
			if opt.EmitMemory != nil {
				opt.EmitMemory(MemoryInfo{Inuse: inuse, OSLimit: osLimit})
			}
		},
		OnConnections: func(snapshot map[string]any) {
			if opt.EmitConnections != nil {
				opt.EmitConnections(ConnectionsSnapshot{Data: snapshot})
			}
		},
		// 看门狗判定崩溃：先清系统代理（防止死端口残留），再推状态。
		OnCrash: func(message string) {
			a.clearSystemProxyIfOurs()
			a.mu.Lock()
			a.state.SystemProxy = false
			a.mu.Unlock()
			_ = a.saveState()
			a.pushStatus(message)
		},
		// 看门狗恢复成功：按用户之前的选择恢复系统代理。
		OnRecovered: func() {
			a.mu.Lock()
			wasSys := a.wasSys && !a.opt.NoSysProxy
			a.mu.Unlock()
			if wasSys {
				if err := a.enableSystemProxy(); err == nil {
					a.mu.Lock()
					a.state.SystemProxy = true
					a.mu.Unlock()
					_ = a.saveState()
				}
			}
			a.pushStatus("")
		},
		OnMessage: func(message string) {
			a.pushStatus(message)
		},
	})
	if err != nil {
		return nil, err
	}
	a.core = ctl

	// 上次会话异常退出遗留的 TUN 开启状态：特权内核不随应用自动恢复（需要重新授权），重置。
	if a.state.Tun {
		log.Printf("[kunpeng] 检测到上次会话遗留的 TUN 开启状态，已重置为关闭（如需请手动开启）")
		a.mu.Lock()
		a.state.Tun = false
		a.mu.Unlock()
	}

	// 首启动导入预置订阅并激活（必须在 a.core 就绪之后）。
	// 导入失败不阻塞启动（可能离线），记录日志即可。
	if err := a.firstRunImport(); err != nil {
		log.Printf("[kunpeng] 预置订阅导入失败（可稍后在 UI 手动添加）: %v", err)
	}

	// 订阅自动更新调度器（后台 goroutine，Shutdown 时干净退出）。
	a.startUpdater()
	return a, nil
}

// AutoConnect 首次进入窗口时按 state.AutoConnect 自动连接（DESIGN §5）。
// 已在运行则只确保系统代理与状态一致。
func (a *App) AutoConnect() {
	a.mu.Lock()
	on := a.state.AutoConnect && a.state.ActiveProfileID != "" && !a.opt.NoSysProxy
	a.mu.Unlock()
	if !on {
		if a.opt.NoSysProxy {
			_ = a.CoreStart() // headless/禁代理模式仍启动内核
		}
		return
	}
	if err := a.CoreStart(); err != nil {
		log.Printf("[kunpeng] 自动连接失败: %v", err)
		return
	}
	if err := a.CoreSetSystemProxy(true); err != nil {
		log.Printf("[kunpeng] 自动开启系统代理失败: %v", err)
	}
}

// Shutdown 进程退出前的清理：停订阅调度器 + 停 TUN 特权内核（如开启）+
// 停内嵌内核 + 清系统代理（仅限本会话写过的）。
func (a *App) Shutdown() {
	a.mu.Lock()
	u := a.updater
	tun := a.tunActive
	a.tunActive = false
	if tun {
		a.state.Tun = false // 特权内核随应用退出，状态重置（重启应用后需手动再开）
	}
	a.mu.Unlock()
	u.closeAndWait()
	if tun {
		if err := a.stopTunCore(); err != nil {
			log.Printf("[kunpeng][tun] 退出时停止特权内核异常: %v", err)
		}
	}
	if a.core != nil {
		a.core.Shutdown()
	}
	a.clearSystemProxyIfOurs()
	if tun {
		_ = a.saveState()
	}
}

// ---------------------------------------------------------------------------
// 状态持久化
// ---------------------------------------------------------------------------

func (a *App) statePath() string    { return filepath.Join(a.dataDir, "state.json") }
func (a *App) profilesPath() string { return filepath.Join(a.dataDir, "profiles.yaml") }
func (a *App) runtimePath() string  { return filepath.Join(a.dataDir, "runtime.yaml") }
func (a *App) profileRawPath(id string) string {
	return filepath.Join(a.dataDir, "profiles", id+".yaml")
}

// loadState 载入（或初始化）state.json。
func (a *App) loadState() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := os.ReadFile(a.statePath())
	if err == nil {
		if jerr := json.Unmarshal(data, &a.state); jerr == nil {
			if a.state.Mode == "" {
				a.state.Mode = "rule"
			}
			if a.state.MixedPort <= 0 {
				a.state.MixedPort = 7890
			}
			if a.state.Secret == "" {
				s, serr := configgen.RandomSecret()
				if serr != nil {
					return serr
				}
				a.state.Secret = s
				return a.saveStateLocked()
			}
			return nil
		}
	}
	// 全新初始化。
	a.state = appState{Mode: "rule", MixedPort: 7890, AutoConnect: true}
	secret, serr := configgen.RandomSecret()
	if serr != nil {
		return serr
	}
	a.state.Secret = secret
	return a.saveStateLocked()
}

func (a *App) saveState() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveStateLocked()
}

func (a *App) saveStateLocked() error {
	data, err := json.MarshalIndent(&a.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.statePath(), data, 0o600)
}

// loadProfiles 载入 profiles.yaml；文件不存在视为首次启动。
func (a *App) loadProfiles() error {
	data, err := os.ReadFile(a.profilesPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 profiles.yaml 失败: %w", err)
	}
	var file struct {
		Profiles []profileRecord `yaml:"profiles"`
	}
	if err := yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("解析 profiles.yaml 失败: %w", err)
	}
	a.mu.Lock()
	a.profiles = file.Profiles
	a.mu.Unlock()
	return nil
}

func (a *App) saveProfilesLocked() error {
	file := struct {
		Profiles []profileRecord `yaml:"profiles"`
	}{Profiles: a.profiles}
	data, err := yaml.Marshal(&file)
	if err != nil {
		return err
	}
	return os.WriteFile(a.profilesPath(), data, 0o644)
}

// writeProfileRawLocked 保存订阅原文。
func (a *App) writeProfileRawLocked(id string, raw []byte) error {
	return os.WriteFile(a.profileRawPath(id), raw, 0o644)
}

// updateProfileLocked 用拉取结果刷新订阅元数据与原文（调用方持锁）。
func (a *App) updateProfileLocked(rec *profileRecord, res *subscription.Result) error {
	if err := a.writeProfileRawLocked(rec.ID, res.Raw); err != nil {
		return err
	}
	rec.NodeCount = res.NodeCount
	rec.Upload, rec.Download, rec.Total = res.UserInfo.Upload, res.UserInfo.Download, res.UserInfo.Total
	rec.Expire = expireRFC3339(res.UserInfo.Expire)
	rec.UpdatedAt = nowRFC3339()
	if rec.Name == "" && res.Name != "" {
		rec.Name = res.Name
	}
	return a.saveProfilesLocked()
}

// firstRunImport profiles.yaml 不存在时导入预置订阅并激活（DESIGN §9）。
func (a *App) firstRunImport() error {
	a.mu.Lock()
	existing := len(a.profiles)
	a.mu.Unlock()
	if existing > 0 {
		return nil
	}
	// 预置订阅来源（开源仓库不含任何私人订阅地址）：
	//   1) <数据目录>/preset.txt（每行一个订阅 URL，首行将被激活）——用户本地自留
	//   2) opt.PresetURL（环境变量 KUNPENG_PRESET_SUB 注入，测试/自动化用）
	url := a.opt.PresetURL
	if url == "" {
		if b, err := os.ReadFile(filepath.Join(a.dataDir, "preset.txt")); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					url = line
					break
				}
			}
		}
	}
	if url == "" {
		return nil
	}
	log.Printf("[kunpeng] 首次启动：导入预置订阅（preset.txt / 环境变量）…")
	p, err := a.ProfileAdd(url, "")
	if err != nil {
		return err
	}
	log.Printf("[kunpeng] 预置订阅已导入：%s（%d 个节点）", p.Name, p.NodeCount)
	return a.ProfileActivate(p.ID)
}

// releaseGeodata 把内嵌 geodata 释放到数据目录（已存在则跳过）。
// 目标名与 mihomo constant/path.go 的查找规则一致：
// geoip.metadb（MMDB() 大小写不敏感匹配）与 GeoSite.dat。
func (a *App) releaseGeodata() {
	pairs := []struct {
		name string
		data []byte
	}{
		{"geoip.metadb", a.opt.GeoIPBytes},
		{"GeoSite.dat", a.opt.GeoSiteBytes},
	}
	for _, p := range pairs {
		if len(p.data) == 0 {
			continue
		}
		dst := filepath.Join(a.dataDir, p.name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, p.data, 0o644); err != nil {
			log.Printf("[kunpeng] 释放 %s 失败: %v", p.name, err)
			continue
		}
		if err := os.Rename(tmp, dst); err != nil {
			log.Printf("[kunpeng] 释放 %s 失败: %v", p.name, err)
		} else {
			log.Printf("[kunpeng] 已释放 geodata: %s（%d 字节）", p.name, len(p.data))
		}
	}
}

// findLocked 按 ID 找订阅（调用方持锁），未找到返回 nil。
func (a *App) findLocked(id string) *profileRecord {
	for i := range a.profiles {
		if a.profiles[i].ID == id {
			return &a.profiles[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowRFC3339() string { return time.Now().Format(time.RFC3339) }

func expireRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
