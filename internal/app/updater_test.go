package app

// updater_test.go —— 订阅自动更新调度器测试：
// 间隔规范化、到期判定、失败顺延、ProfileEdit 的 autoUpdate 持久化。
// 不真等 tick（注入式验证内部函数），也不触网（ProfileEdit 仅改名/改设置）。

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeUpdateInterval(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultAutoUpdateIntervalMin},  // 缺省 → 1440
		{-5, DefaultAutoUpdateIntervalMin}, // 非法 → 1440
		{10, MinAutoUpdateIntervalMin},     // 低于下限 → 30
		{29, MinAutoUpdateIntervalMin},     // 同上
		{30, 30},                           // 下限原样
		{60, 60},                           // 正常值原样
		{1440, 1440},                       // 默认值原样
	}
	for _, c := range cases {
		if got := normalizeUpdateInterval(c.in); got != c.want {
			t.Fatalf("normalizeUpdateInterval(%d) = %d 期望 %d", c.in, got, c.want)
		}
	}
}

func TestDueAutoUpdateIDs(t *testing.T) {
	a, id := newSmokeApp(t)
	now := time.Now()

	setRec := func(updatedAt string, autoUpdate bool, interval int) {
		a.mu.Lock()
		for i := range a.profiles {
			if a.profiles[i].ID == id {
				a.profiles[i].UpdatedAt = updatedAt
				a.profiles[i].AutoUpdate = autoUpdate
				a.profiles[i].UpdateIntervalMin = interval
			}
		}
		a.mu.Unlock()
	}

	// 未开启自动更新 → 不到期
	setRec(now.Add(-2*time.Hour).Format(time.RFC3339), false, 60)
	if got := a.dueAutoUpdateIDs(now); len(got) != 0 {
		t.Fatalf("autoUpdate=false 不应到期: %v", got)
	}

	// 到期：updatedAt(2h 前) + 60min < now
	setRec(now.Add(-2*time.Hour).Format(time.RFC3339), true, 60)
	if got := a.dueAutoUpdateIDs(now); len(got) != 1 || got[0] != id {
		t.Fatalf("应到期: %v", got)
	}

	// 未到期：updatedAt(30min 前) + 60min > now
	setRec(now.Add(-30*time.Minute).Format(time.RFC3339), true, 60)
	if got := a.dueAutoUpdateIDs(now); len(got) != 0 {
		t.Fatalf("未到期却命中: %v", got)
	}

	// 间隔下限收敛：interval=10 按 30 分钟计算
	setRec(now.Add(-40*time.Minute).Format(time.RFC3339), true, 10)
	if got := a.dueAutoUpdateIDs(now); len(got) != 1 {
		t.Fatalf("interval 收敛后应到期（40min > 30min）: %v", got)
	}
	setRec(now.Add(-20*time.Minute).Format(time.RFC3339), true, 10)
	if got := a.dueAutoUpdateIDs(now); len(got) != 0 {
		t.Fatalf("interval 收敛后不应到期（20min < 30min）: %v", got)
	}

	// 失败顺延：到期但在顺延期内 → 跳过；顺延过期 → 恢复命中
	setRec(now.Add(-2*time.Hour).Format(time.RFC3339), true, 60)
	a.updater.deferID(id, now.Add(10*time.Minute))
	if got := a.dueAutoUpdateIDs(now); len(got) != 0 {
		t.Fatalf("顺延期内不应命中: %v", got)
	}
	if got := a.dueAutoUpdateIDs(now.Add(11 * time.Minute)); len(got) != 1 {
		t.Fatalf("顺延过期应恢复命中: %v", got)
	}
	// 清除顺延（成功更新/编辑后）→ 立即恢复
	a.updater.clearDeferred(id)
	if got := a.dueAutoUpdateIDs(now); len(got) != 1 {
		t.Fatalf("清除顺延后应命中: %v", got)
	}

	// UpdatedAt 非法 → 跳过（不崩溃、不命中）
	setRec("not-a-time", true, 60)
	if got := a.dueAutoUpdateIDs(now); len(got) != 0 {
		t.Fatalf("非法时间应跳过: %v", got)
	}
}

// TestProfileEditAutoUpdatePersist 五参数 Edit 的持久化与规范化。
func TestProfileEditAutoUpdatePersist(t *testing.T) {
	a, id := newSmokeApp(t)

	// 旧数据（profiles.yaml 无 autoUpdate 字段）→ 载荷缺省 false/1440
	p0, err := a.ProfileCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if p0.AutoUpdate || p0.UpdateIntervalMin != DefaultAutoUpdateIntervalMin {
		t.Fatalf("旧数据缺省不符: %+v", p0)
	}

	// 开启自动更新 + 超短间隔 → 下限收敛 30 并持久化
	p1, err := a.ProfileEdit(id, "", "", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !p1.AutoUpdate || p1.UpdateIntervalMin != MinAutoUpdateIntervalMin {
		t.Fatalf("Edit 载荷不符: %+v", p1)
	}
	pdata, err := os.ReadFile(a.profilesPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pdata), "autoUpdate: true") || !strings.Contains(string(pdata), "updateIntervalMin: 30") {
		t.Fatalf("profiles.yaml 未持久化: %s", pdata)
	}
	// 重新载入（loadProfiles）后读出一致
	if err := a.loadProfiles(); err != nil {
		t.Fatal(err)
	}
	if p := a.ProfilesList(); len(p) != 1 || !p[0].AutoUpdate || p[0].UpdateIntervalMin != MinAutoUpdateIntervalMin {
		t.Fatalf("重新载入后不符: %+v", p)
	}

	// 关闭自动更新 + 0 间隔 → 默认 1440
	p2, err := a.ProfileEdit(id, "", "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p2.AutoUpdate || p2.UpdateIntervalMin != DefaultAutoUpdateIntervalMin {
		t.Fatalf("关闭后载荷不符: %+v", p2)
	}
	if got := a.dueAutoUpdateIDs(time.Now()); len(got) != 0 {
		t.Fatalf("关闭自动更新后不应有到期项: %v", got)
	}
}

// TestAutoUpdaterLoopEndToEnd 全链路：注入 100ms tick，订阅到期后调度器
// 真实执行 ProfileUpdate（httptest 拉取 → 原文/元数据落盘 → updatedAt 刷新）。
func TestAutoUpdaterLoopEndToEnd(t *testing.T) {
	oldTick := updaterTickInterval
	updaterTickInterval = 100 * time.Millisecond // 必须在 newSmokeApp 前注入（ticker 启动时读取）
	defer func() { updaterTickInterval = oldTick }()

	a, id := newSmokeApp(t)

	var fetches atomic.Int32
	newSub := `proxies:
  - name: "🇺🇸 更新后节点"
    type: ss
    server: us.example.com
    port: 8388
    cipher: aes-128-gcm
    password: x
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		_, _ = w.Write([]byte(newSub))
	}))
	defer srv.Close()

	// 构造到期订阅：2 小时前更新过 + 60 分钟间隔 → now > updatedAt+interval。
	rawAt := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	a.mu.Lock()
	for i := range a.profiles {
		if a.profiles[i].ID == id {
			a.profiles[i].URL = srv.URL
			a.profiles[i].UpdatedAt = rawAt
			a.profiles[i].AutoUpdate = true
			a.profiles[i].UpdateIntervalMin = 60
		}
	}
	a.mu.Unlock()

	// 等调度器循环真实执行（tick 100ms + 拉取），最长 5s。
	deadline := time.Now().Add(5 * time.Second)
	updated := false
	for time.Now().Before(deadline) {
		a.mu.Lock()
		rec := a.findLocked(id)
		done := rec != nil && rec.UpdatedAt != rawAt && rec.NodeCount == 1
		a.mu.Unlock()
		if done {
			updated = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !updated {
		t.Fatalf("调度器未在 5s 内完成自动更新（fetches=%d）", fetches.Load())
	}
	if fetches.Load() == 0 {
		t.Fatal("订阅服务器未被请求")
	}
	// 原文落盘验证。
	raw, err := os.ReadFile(a.profileRawPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "更新后节点") {
		t.Fatalf("订阅原文未更新: %s", raw)
	}
	// 更新后 updatedAt 刷新 → 不再到期。
	a.mu.Lock()
	rec := a.findLocked(id)
	parsed, _ := time.Parse(time.RFC3339, rec.UpdatedAt)
	a.mu.Unlock()
	if got := a.dueAutoUpdateIDs(parsed.Add(10 * time.Minute)); len(got) != 0 {
		t.Fatalf("更新后不应立即再次到期: %v", got)
	}
}
