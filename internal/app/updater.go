package app

// 订阅自动更新调度器（DESIGN §5 ProfilesService）：
//   - 每 tickInterval 扫描一次 profiles，对开启 autoUpdate 的订阅按
//     「updatedAt + interval <= now」判定到期，到期调 ProfileUpdate（拉取+落盘+元数据刷新，
//     激活订阅且内核可用时热应用——含 TUN 特权内核 REST 重载）；
//   - 失败记日志并按该订阅自身的 interval 顺延重试（不密集重试）；
//   - ProfileEdit / ProfileUpdate 成功后重算该订阅的调度（清除失败顺延标记，
//     updatedAt 刷新后到期时间自然后移）；
//   - 应用退出（Shutdown）时干净退出。
//
// 调度只做「到期判定」，不改变内核生命周期：内核未运行时照常拉取落盘。

import (
	"log"
	"sync"
	"time"
)

// updaterTickInterval 调度器扫描周期（1 分钟粒度；测试可注入更短值）。
var updaterTickInterval = time.Minute

// 自动更新间隔约束。
const (
	// MinAutoUpdateIntervalMin 自动更新最小间隔（分钟），低于该值按该值处理。
	MinAutoUpdateIntervalMin = 30
	// DefaultAutoUpdateIntervalMin 自动更新默认间隔（分钟）= 24 小时。
	DefaultAutoUpdateIntervalMin = 1440
)

// normalizeUpdateInterval 规范化自动更新间隔：<=0 视为默认 1440；<30 收敛到下限 30。
func normalizeUpdateInterval(min int) int {
	if min <= 0 {
		return DefaultAutoUpdateIntervalMin
	}
	if min < MinAutoUpdateIntervalMin {
		return MinAutoUpdateIntervalMin
	}
	return min
}

// autoUpdater 订阅自动更新调度器。
type autoUpdater struct {
	stop chan struct{}
	done chan struct{}

	mu         sync.Mutex
	deferUntil map[string]time.Time // 失败顺延：订阅 id → 最早重试时间
}

// startUpdater 启动调度器（应用初始化末尾调用一次）。
func (a *App) startUpdater() {
	u := &autoUpdater{
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		deferUntil: map[string]time.Time{},
	}
	a.mu.Lock()
	a.updater = u
	a.mu.Unlock()
	go u.loop(a)
}

// loop 主循环：周期扫描到期订阅并逐个更新。
func (u *autoUpdater) loop(a *App) {
	defer close(u.done)
	tick := time.NewTicker(updaterTickInterval)
	defer tick.Stop()
	for {
		select {
		case <-u.stop:
			return
		case <-tick.C:
			u.runDue(a)
		}
	}
}

// runDue 更新所有到期订阅（顺序执行，避免并发拉取打满订阅服务器）。
func (u *autoUpdater) runDue(a *App) {
	for _, id := range a.dueAutoUpdateIDs(time.Now()) {
		select {
		case <-u.stop:
			return
		default:
		}
		interval := a.autoUpdateIntervalOf(id)
		log.Printf("[kunpeng][updater] 自动更新订阅 %s（间隔 %d 分钟）…", id, interval)
		if _, err := a.ProfileUpdate(id); err != nil {
			retryAt := time.Now().Add(time.Duration(interval) * time.Minute)
			log.Printf("[kunpeng][updater] 订阅 %s 自动更新失败，顺延到 %s 重试: %v",
				id, retryAt.Format("15:04"), err)
			u.deferID(id, retryAt)
			continue
		}
		u.clearDeferred(id)
	}
}

// dueAutoUpdateIDs 返回当前到期待更新的订阅 id（纯查询，单测直接调用）。
// 到期判定 = updatedAt + normalize(interval) <= now 且不在失败顺延期内。
// UpdatedAt 无法解析的订阅跳过（等待用户手动更新以修正元数据）。
func (a *App) dueAutoUpdateIDs(now time.Time) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, 4)
	for i := range a.profiles {
		rec := &a.profiles[i]
		if !rec.AutoUpdate {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339, rec.UpdatedAt)
		if err != nil {
			continue
		}
		interval := time.Duration(normalizeUpdateInterval(rec.UpdateIntervalMin)) * time.Minute
		if now.Before(updatedAt.Add(interval)) {
			continue
		}
		if u := a.updater; u != nil {
			u.mu.Lock()
			until, deferred := u.deferUntil[rec.ID]
			u.mu.Unlock()
			if deferred && now.Before(until) {
				continue
			}
		}
		out = append(out, rec.ID)
	}
	return out
}

// autoUpdateIntervalOf 返回订阅的规范化自动更新间隔（分钟）；订阅不存在返回默认值。
func (a *App) autoUpdateIntervalOf(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.profiles {
		if a.profiles[i].ID == id {
			return normalizeUpdateInterval(a.profiles[i].UpdateIntervalMin)
		}
	}
	return DefaultAutoUpdateIntervalMin
}

// deferID 设置失败顺延标记。
func (u *autoUpdater) deferID(id string, until time.Time) {
	u.mu.Lock()
	u.deferUntil[id] = until
	u.mu.Unlock()
}

// clearDeferred 清除失败顺延标记（编辑/更新成功后重算调度）。
func (u *autoUpdater) clearDeferred(id string) {
	u.mu.Lock()
	delete(u.deferUntil, id)
	u.mu.Unlock()
}

// rescheduleProfile ProfileEdit / 手动 ProfileUpdate 成功后的重算入口：
// 清除顺延标记，到期时间由刷新后的 updatedAt 自然重算。
func (a *App) rescheduleProfile(id string) {
	a.mu.Lock()
	u := a.updater
	a.mu.Unlock()
	if u != nil {
		u.clearDeferred(id)
	}
}

// closeAndWait 停止调度器并等待退出（Shutdown 用；最多等 3s 防止退出卡住）。
func (u *autoUpdater) closeAndWait() {
	if u == nil {
		return
	}
	select {
	case <-u.stop: // 已关闭
	default:
		close(u.stop)
	}
	select {
	case <-u.done:
	case <-time.After(3 * time.Second):
		log.Printf("[kunpeng][updater] 调度器未在 3s 内退出（正在拉取中，随进程结束终止）")
	}
}
