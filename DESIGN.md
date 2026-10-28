# 鲲鹏 Clash（KunpengClash）设计文档

> 用 [mygo](https://github.com/egoist/mygo)（egoist 的 Go 桌面应用框架）+ mihomo 内核（以库形式嵌入）
> 实现的类 FlClash macOS 代理客户端。单 Go 二进制 + 系统 WKWebView 前端。

## 1. 背景与目标

用户的 FlClash 频繁断线，且断线后整机无法上网（典型原因：内核异常退出后系统代理残留指向已死的端口）。
本应用第一优先级是**连接可靠性**，第二是覆盖 FlClash 的高频功能，UI 参考 FlClash 深色主题。

### 必达目标（v1，今晚交付）

1. 从订阅 URL 拉取 mihomo YAML（实测返回完整 clash 配置 + `subscription-userinfo` 流量头）。
2. 代理内核可用：mixed-port **7890**（HTTP+SOCKS），浏览器走系统代理即可访问 Google/X。
3. UI 五个页面：仪表盘 / 代理 / 配置 / 实时连接 / 工具（参考 FlClash 截图布局与深色风格）。
4. 首次启动自动导入预置订阅并激活。
5. **可靠性**：
   - 内核意外退出 → 立即清除系统代理 + 自动重启内核（指数退避，最多 5 次）→ 成功后按用户之前的选择恢复系统代理；
   - 应用正常退出/停止内核 → 清除系统代理；
   - 出站模式支持「自动选择」(url-test 组) + 手动选择；健康检查每 60s 测当前出口延迟，连续 3 次失败时若为自动模式则自动切换最优节点，并以 StatusEvent 通知前端。

### v1.1（后续）

- TUN（虚拟网卡）模式：需要 root，通过 osascript 管理员授权运行内核；v1 仅在 UI 上展示但禁用（提示"需管理员权限，暂未开放"）。
- 菜单栏 tray 快捷开关、开机自启。

## 2. 架构

```
┌─────────────────────────── 单个 Go 进程 ───────────────────────────┐
│  mygo (WKWebView 窗口, Bind IPC, 菜单/生命周期, 打包)               │
│      │ typed bindings (src/mygo.ts)                                │
│  ┌───┴──────────────────────────────────────────────────────┐     │
│  │ internal/app —— 服务层（main.go 契约的权威实现）           │     │
│  │   ProfilesService  订阅 CRUD/刷新/激活                      │     │
│  │   CoreService      内核生命周期/模式/系统代理/端口            │     │
│  │   ProxyService     mihomo external-controller HTTP 桥接     │     │
│  │   AppService       工具杂项                                  │     │
│  │   事件推送: traffic/log/connections/memory/status (每秒)     │     │
│  ├──────────────────────────────────────────────────────────┤     │
│  │ internal/subscription  订阅拉取+解析（YAML+userinfo）        │     │
│  │ internal/configgen     生成 mihomo 运行配置                  │     │
│  │ internal/corectl       mihomo 嵌入(库调用)+看门狗+健康检查    │     │
│  │ internal/sysproxy      networksetup 系统代理写入/清除/检测    │     │
│  ├──────────────────────────────────────────────────────────┤     │
│  │ mihomo v1.19.33 (library)                                  │     │
│  │   executor.ParseWithBytes + ApplyConfig → tunnel/内核       │     │
│  │   hub/route.ReCreateServer → 127.0.0.1:9097 (secret)       │     │
│  │   mixed-port 7890 ← 所有应用流量                             │     │
│  └──────────────────────────────────────────────────────────┘     │
└────────────────────────────────────────────────────────────────────┘
```

前端（React 19 + Vite + TS）只通过 `src/mygo.ts`（mygo generate 生成）调用 Go 服务，
**不直接访问 mihomo REST**（避免 secret/CORS/WebSocket 管理分散在前端）。

## 3. 技术栈与关键事实

| 组件 | 版本 | 说明 |
|---|---|---|
| mygo | v0.7.1 | 框架；`mygo dev` 开发、`mygo build` 产出 .app+DMG、`mygo generate` 产出 TS client |
| mihomo | v1.19.33 | 以库嵌入：`constant.SetHomeDir` / `executor.ParseWithBytes` / `executor.ApplyConfig` / `hub/route.ReCreateServer` |
| Go | 1.27.3（toolchain 自动） | 无 cgo |
| 前端 | React 19 + Vite 7 + TS | 深色主题，无 UI 框架依赖 |
| geodata | GEOIP.metadb 9.6MB + GEOSITE.dat 4.4MB | 已拷入 `resources/geodata/`（来自本机 FlClash 数据目录），安装时释放到数据目录 |

### 网络环境（重要）

- 本机 shell 有 `http_proxy/https_proxy/all_proxy=127.0.0.1:7890` 残留，而当前 7890 无服务 → **所有 shell 命令必须先 unset 这三个变量**（项目脚本已内置）。
- GitHub / proxy.golang.org 直连超时；**Go 依赖一律走 `GOPROXY=https://goproxy.cn,direct`**。
- npm 官方源直连可用，无需镜像。
- 订阅地址直连可用（Cloudflare），UA 用 `clash.meta/…` 返回 mihomo YAML。

## 4. 目录结构

```
kunpeng-clash/
├── main.go                # IPC 契约权威定义 + 入口（服务=骨架，实现委托 internal/app）
├── mygo.config.ts         # mygo 打包配置（name/identifier/devUrl/bindings）
├── go.mod                 # module kunpeng
├── package.json           # npm scripts: dev/build/generate/typecheck
├── internal/
│   ├── app/               # 服务实现：组装 subscription/corectl/sysproxy
│   ├── subscription/      # 订阅拉取与解析
│   ├── configgen/         # mihomo 配置生成
│   ├── corectl/           # mihomo 库驱动、看门狗、健康检查、REST 桥接
│   └── sysproxy/          # macOS 系统代理
├── src/                   # React 前端（mygo.ts 为生成文件勿手改）
├── resources/
│   ├── icon.png
│   └── geodata/           # GEOIP.metadb / GEOSITE.dat（go:embed）
├── scripts/               # 构建/测试脚本（均自带 unset 代理变量）
└── build/                 # mygo build 输出（.app/.dmg）
```

数据目录（运行时）：`~/Library/Application Support/KunpengClash/`
- `profiles.yaml` 订阅元数据；`profiles/<id>.yaml` 订阅原文；`runtime.yaml` 最终生成的内核配置
- `geoip.metadb` / `geosite.dat`（从 embed 释放）；`state.json` 内核状态/模式/端口/系统代理开关
  记忆 + `whitelist`（直连白名单）+ `customProxies`（自定义节点）+ `routeRules`（分流规则，
  按订阅 ID 挂载，跟着配置走）+ `tun`（虚拟网卡开关）
- `tun/`（TUN 开启时创建）：特权内核 HomeDir——geoip/geosite 硬链接、runtime.yaml 镜像、
  quit 退出标记、root 属主的 cache.db

## 5. IPC 契约

**权威定义在 main.go**（方法签名 + JSON 字段），此处只写行为语义。改签名必须重跑 `npm run generate`。

### 事件

- `traffic`：每 1s `{up,down}` 字节/秒（内核未运行时不推送）。
- `log`：mihomo 日志行（info 级以上），环形缓冲 500 条；`AppService.GetLogs` 供页面打开时回填。
- `connections`：每 1s 推送 mihomo `/connections` 快照 `{data: <原生JSON对象>}`。
- `memory`：每 5s 内核内存。
- `status`：任何内核状态变化（启动成功/停止/崩溃/模式切换/系统代理变化）推送完整 CoreStatus。

### ProfilesService 语义

- `Add(url, name)`：GET 订阅（UA: `clash.meta/1.19.0 KunpengClash/0.1.0`），解析 `subscription-userinfo`
  头（`upload=;download=;total;expire=`，分号分隔）与 YAML `proxies` 数组；存原文；返回 Profile。
  HTTP 失败返回可读错误（含状态码）。
- `Update(id)`：重新拉取并覆盖原文 + userinfo。
- `Activate(id)`：用订阅原文的 `proxies` 生成运行配置（见 §6），写 `runtime.yaml`，
  内核在运行则热重载（ApplyConfig），未运行则启动。激活状态持久化。
- `Edit(id, name, url, autoUpdate, updateIntervalMin)`：改名；url 变更则立即用新地址重新拉取
  （原文/流量/nodeCount/updatedAt 全刷新）；持久化 autoUpdate/updateIntervalMin
  （间隔规范化：<=0 视为默认 1440 分钟，<30 收敛到下限 30）；
  若该订阅是激活订阅且内核可用 → 重新生成 runtime.yaml 并热应用。拉取失败则整体报错不落盘。
  保存成功后重算该订阅的自动更新调度。
- **自动更新调度（internal/app/updater.go）**：应用启动即后台 goroutine，每 1 分钟扫描：
  `updatedAt + interval <= now` 判定到期 → 调 ProfileUpdate（拉取+落盘+元数据刷新，
  激活订阅且内核可用时热应用）。失败记日志并按该订阅自身 interval 顺延（不密集重试）；
  Edit/手动 Update 成功后清除顺延标记重算。内核未运行也照常更新内容。退出时干净退出。
- 预置：首启动（profiles.yaml 不存在）自动 Add 预置订阅 URL（见 §9）并 Activate，但不自动启动内核（由 autoConnect 决定，默认 true → 启动内核+系统代理）。

### NodesService 语义（自定义节点，独立于订阅，重启保留）

- `AddFromLinks(raw)`：按行解析 vless:// vmess:// ss://（两种形态） hysteria2://(hy2://) trojan://
  分享链接（internal/nodelink），转换为 mihomo proxy 配置追加到 state.json `customProxies`
  （重名跳过、非法行跳过计入失败数、fragment 缺失自动命名「自定义节点N」），
  保存后重新生成 runtime.yaml 并在内核运行时热应用；返回成功新增条数。
- `List()`：返回全部自定义节点名。`Remove(name)`：删除并热应用。
- 自定义节点**不经过 IsInfoNode 过滤**（用户手动添加的都保留）；与订阅节点重名时自动加
  「-2/-3」后缀保证 mihomo 不因重名拒绝整份配置；无订阅时仅凭自定义节点也能生成配置。

### CoreService 白名单语义（直连白名单）

- `GetWhitelist()` / `SetWhitelist(domains)`：state.json `whitelist` 持久化。
  Set 全量替换：规范化（trim/去空/去重/小写，非法 CIDR 或带协议头路径报错，上限 200 条）→
  保存 → 重新生成 runtime.yaml → 内核运行时热应用 → StatusEvent。
- 每项形态与转换（internal/configgen.WhitelistToRules）：
  `example.com` → DOMAIN-SUFFIX（含子域）；`*.example.com`/`+.example.com` 等价上一条；
  `a.example.com`（≥2 个点）→ DOMAIN 精确；`1.2.3.0/24` → IP-CIDR,…,no-resolve。
- rule 模式下白名单规则插入在私有/环回网段 4 条之后、`DOMAIN-SUFFIX,cn` 之前；
  global/direct 模式不生效（全直连/全代理场景无意义）。

### RulesService 语义（分流规则：域名 → 指定节点组，挂在订阅下）

典型场景：某些站点（`*.chatgpt.com`、`*.claude.ai`、`*.gemini.com`…）只能用特定地区节点访问。
规则以订阅 ID 为键存 state.json `routeRules`（**跟着配置走**：激活哪个订阅，哪份规则生效；
删除订阅时连规则一起清除）。

- `List(profileID)`：返回该订阅挂载的全部分流规则。
- `Save(profileID, rules)`：全量替换。校验规范化（configgen.NormalizeRouteGroups）：
  组数 ≤20、每组域名 ≤200 / 节点 ≤100、组名非空且不含逗号/斜杠（规则串按逗号切分）、
  组名不重复、每组至少 1 域名 1 节点；域名规范化同白名单。→ 持久化 →
  该订阅是激活订阅且内核可用时热应用 → StatusEvent。
- `NodeNames(profileID)`：编辑器可选节点 = 该订阅真实节点（过滤机场信息占位节点）+ 自定义节点，
  与生成配置时进入策略组的名单一致。
- 每条规则 RouteRule：`{id, name, domains[], nodes[], auto}`。
  生成时每组一个 mihomo 策略组：`auto=false` → select（代理页手动挑），`true` → url-test
  （组内测速自动选最快，间隔/容差同 ♻️ 自动选择）。**组内只有所选节点**——不含 DIRECT、
  不回落到任何其他组，保证这些域名只能在这一组节点里跑。
- 生成容错（configgen.buildRouteGroups）：节点只保留真实存在于本次配置的
  （订阅更新后改名/消失的条目自动失效）；整组节点失效则连组带规则跳过——绝不生成
  「规则引用不存在的代理」让 mihomo 拒绝整份配置；组名与固定组/节点名/其他组冲突时自动加
  `-2/-3` 后缀，域名规则跟随改名后的组名。
- 规则插入位置：私有/环回网段 4 条直连之后、白名单与国内分流之前（对这些域名的定向是最高
  优先级意图）；仅 rule 模式生效，与白名单语义一致。

### CoreService 语义

- `Start`：无激活订阅则报错；已运行则幂等成功。启动流程见 §7。
- `Stop`：停内核（executor.Shutdown 或重建空配置）→ **无条件清系统代理** → StatusEvent。
- `SetSystemProxy(true)`：写入系统代理（HTTP/HTTPS/SOCKS 全部指向 127.0.0.1:mixedPort，排除域名见 §8）。
- `SetMode` / `SetPort`：持久化 + 热应用（重生成 runtime.yaml 后 ApplyConfig；
  TUN 模式下为 REST PUT /configs?force=true 重载，见 §7b）。
- `SetTun(on)`：虚拟网卡开关（见 §7b）。所有变化都发 StatusEvent。

### ProxyService 语义

直转发 external-controller（127.0.0.1:9097，带 Authorization: Bearer secret）：
- `All` → GET /proxies；`Select` → PUT /proxies/{group} body `{"name":...}`；
- `GroupDelay` → GET /proxies/{group}/delay?url=…&timeout=…；`ProxyDelay` → GET /proxies/{name}/delay?…
  （url 默认 `http://www.gstatic.com/generate_204`，timeout 默认 5000）；
- `Rules` → GET /rules。内核未运行返回错误 `"内核未运行"`（前端据此禁用交互）。

### AppService 语义

- `TestURL(url)`：通过 `http://127.0.0.1:{mixedPort}` 作为代理请求 url（5s 超时），返回 HTTP 状态码；失败返回 0 + 错误。
- `OpenDataDir`：`open <dataDir>`。

## 6. 内核配置生成（internal/configgen）

输入：订阅 YAML 原文（只需其中的 `proxies:`）。输出 runtime.yaml：

```yaml
mixed-port: 7890            # 用户可改
allow-lan: true             # 局域网共享（ FlClash 默认如此，端口 7890 已被用户指定为局域网端口）
bind-address: '*'
mode: rule
log-level: info
external-controller: 127.0.0.1:9097
secret: <随机生成一次并持久化>
ipv6: false
profile: { store-selected: true, store-fake-ip: true }
dns:
  enable: true
  ipv6: false
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-filter: ['*.lan', '*.local', '+.msftconnecttest.com', '+.msftncsi.com']
  default-nameserver: [223.5.5.5, 119.29.29.29]
  nameserver: ['https://doh.pub/dns-query', 'https://dns.alidns.com/dns-query']
  fallback: ['https://1.1.1.1/dns-query', 'https://dns.google/dns-query']
  fallback-filter: { geoip: true, geoip-code: CN, ipcidr: [240.0.0.0/4] }
tun: { enable: false, stack: mixed, dns-hijack: ['any:53'], auto-route: true, auto-detect-interface: true }  # v1 固定 false
proxies: <订阅原文 proxies 原样合入>
proxy-groups:
  - name: 🚀 节点选择
    type: select
    proxies: [♻️ 自动选择, DIRECT, <全部节点名…>]
  - name: ♻️ 自动选择
    type: url-test
    url: http://www.gstatic.com/generate_204   # 早高峰实测 http 版通过率高于 https
    interval: 300
    tolerance: 80
    proxies: <全部节点名>
  - <分流组…（RulesService 配置：每组只含所选节点，select/url-test）>
rules:
  # （rule 模式）私有/环回网段 4 条直连在最前，其后依次：
  #   IP-CIDR,127.0.0.0/8 / 10.0.0.0/8 / 172.16.0.0/12 / 192.168.0.0/16 ,DIRECT,no-resolve
  #   <分流规则…（域名 → 指定节点组，优先级最高，见 §5 RulesService）>
  #   <白名单规则…（configgen.WhitelistToRules，用户可配）>
  - DOMAIN-SUFFIX,cn,DIRECT
  - DOMAIN-KEYWORD,baidu,DIRECT
  - DOMAIN-KEYWORD,tencent,DIRECT
  - GEOSITE,cn,DIRECT
  - GEOIP,CN,DIRECT,no-resolve      # fallback-filter 已有 geoip; no-resolve 避免假 IP 问题 → 实测若影响再调整
  - MATCH,🚀 节点选择
```

注意：节点名可能含 YAML 特殊字符，用 yaml.v3 结构化序列化而非字符串拼接。
mihomo 库自带 yaml 依赖（gopkg.in/yaml.v3 在其 go.sum 内）可直接使用。

## 7. 内核启动与看门狗（internal/corectl）

1. `constant.SetHomeDir(dataDir)`（geodata 在此目录）→ `executor.ParseWithBytes(runtime.yaml)` → **`hub.ApplyConfig(cfg)`**。
   （实现事实：`hub.ApplyConfig` = `route.ReCreateServer(cfg.Controller)` + `executor.ApplyConfig(cfg, true)`，
   比 DESIGN 初稿手工 ReCreateServer 更贴近 mihomo 官方入口；external-controller/secret 由配置字段驱动。）
2. 停止内核：先以「端口全 0」配置热应用一次关掉 mixed 等入站监听
   （**库源码事实：`executor.Shutdown()` → `listener.Cleanup()` 只关 TUN，不关 mixed/http/socks 监听**），
   再 `executor.Shutdown()` + `route.ReCreateServer(&route.Config{})`（空地址 = 关闭 API）。
3. 事件采集：
   - **决策（已实现）：同进程 HTTP 客户端消费 external-controller。实现上连 WebSocket 都不需要——
     `/traffic` `/logs` `/memory` 对非 Upgrade 请求就是无限 JSON-lines 流（服务端逐行 Flush）；
     `/connections` 非 WS 请求返回单份快照 → 客户端每 1s 轮询。断线自动重连（2s，先等 /version 恢复）。**
4. 看门狗：监测内核健康（对 /version 每 5s 轮询）。连续 2 次失败判定异常 → 停事件流 → **清系统代理** → 指数退避重启（1s,2s,4s,8s,16s，最多 5 次）→ 成功后若之前系统代理开着则重开。每次状态变化推 StatusEvent。
   看门狗与事件流使用两条独立生命周期：崩溃退避重启期间看门狗必须存活，只有 Stop/Shutdown 终止它。
5. 健康检查：每 60s 对 🚀 节点选择 当前出口 GET /proxies/{当前节点}/delay；连续 3 次失败 → 记录并（若用户当前组是自动选择或节点选择选中了 ♻️ 自动选择则由 url-test 自动处理；否则）推 StatusEvent(message=出口不可用) 并将 🚀 节点选择 切到 ♻️ 自动选择。

## 7b. 虚拟网卡 TUN（特权子进程模式）

GUI 进程无 root 不能创建 utun。方案：内核切换为由 osascript 管理员授权启动的**特权子进程**
（`--config-only` 独立模式），GUI 经**同一 external-controller 地址与密钥**继续控制——
REST 桥接与事件流（traffic/log/memory/connections）在内核 Handoff 后自动重连到特权内核，无感切换。

- **开启 `SetTun(true)`**：state.tun=true 持久化 → 生成 tun.enabled 的 runtime.yaml →
  内嵌内核 `Handoff`（停 executor/看门狗，**保留事件流**；系统代理保持，与 TUN 共存）→
  准备 `<dataDir>/tun/`（geoip.metadb/GEOSITE.dat 硬链接，失败拷贝；cache.db 由 root 自建）→
  写配置镜像 `tun/runtime.yaml`（mihomo `IsSafePath` 要求重载路径在 HomeDir 内）→
  osascript 提权启动（用户取消则回滚：state.tun=false + 重启内嵌内核）→
  轮询 /version（20s）→ StatusEvent(Tun=true)。
- **关闭 `SetTun(false)`**：写 `tun/quit` 标记（GUI 可写，root 进程 500ms 轮询自杀，免二次提权）→
  轮询退出（10s 超时则提权 `pkill -f config-only` 兜底）→ 清标记 → 重启内嵌内核 → StatusEvent。
- **TUN 模式下的行为**：ProxyService 全部经同地址 REST 直接可用；`hotApply`/`ProfileActivate`
  走 REST `PUT /configs?force=true`（path=tun/runtime.yaml）重载；CoreStop 等价关 TUN；
  CoreRestart 重新提权启动（需再次授权）；CoreStart 幂等空转。
- **退出清理**：Shutdown 时若 TUN 开启 → 写 quit 等待退出，state.tun 重置为 false
  （特权内核不随应用自动恢复，重启应用后需手动再开）。
- **特权子进程约定（main.go --config-only）**：跳过一切 state/订阅/系统代理逻辑，
  `SetHomeDir(--data-dir)` 后按 `--config` 启动，500ms 轮询 `--quit-file` 存在即优雅退出；
  绝不写数据目录根下任何文件；强制 no-sysproxy 语义。
- 启动时若发现 state.tun 遗留为 true（上次异常退出），自动重置为关闭并记日志。

## 8. 系统代理（internal/sysproxy）

- 检测活动网络服务：`networksetup -listallnetworkservices`，过滤掉 `*` 开头行；对每个服务写：
  - `networksetup -setwebproxy <svc> 127.0.0.1 <port>`
  - `networksetup -setsecurewebproxy <svc> 127.0.0.1 <port>`
  - `networksetup -setsocksfirewallproxy <svc> 127.0.0.1 <port>`
  - `networksetup -setproxybypassdomains <svc> <排除列表…>`
- 清除：**`-setwebproxystate <svc> off` / `-setsecurewebproxystate <svc> off` / `-setsocksfirewallproxystate <svc> off`**。
  （实现事实：本机 macOS 上 `-setwebproxy <svc> off` 会把字面量 "off" 当作代理主机写入并保持
  `HTTPEnable: 1`，导致流量指向死地址——必须用 `*proxystate` 三件套关闭。）
- 默认排除域名（参考 FlClash 截图）：`127.0.0.1, localhost, 192.168.0.0/16, 10.0.0.0/8, 172.16.0.0/12, *.local, *.cn` 等（写成可配置数组存 state.json）。
- 实现：exec.Command，失败返回 stderr；写前检测服务存在。
- 停止/退出时只清理本会话写过的代理（`--no-sysproxy` 会话绝不触碰系统设置）。

## 9. 预置订阅（首启动自动导入）

订阅 URL 含私人 token，**不写入源码仓库**。首启动且本地无订阅时，按以下优先级取预置订阅：
环境变量 `KUNPENG_PRESET_SUB` → 构建时 `-ldflags "-X main.presetSubscriptionURL=..."` → 空（不导入，用户在配置页手动添加）。
名称取 content-disposition 文件名，仅写入用户本机 profiles.yaml。

## 10. 打包

- `npm run build` → `mygo build` → `build/darwin-arm64/KunpengClash.app` + `kunpeng-clash-0.1.0-arm64.dmg`。
- geodata 已在 resources 内由 go:embed 携带；首启动释放到数据目录。
- 无签名（本机使用）；若 mygo build 需要，用 ad-hoc。

## 11. 测试计划

1. **单元/集成（node 脚本，scripts/test_*.mjs）**：
   - 订阅拉取解析（真实 URL）；configgen 输出可被 mihomo ParseWithBytes 接受。
2. **端到端（scripts/smoke_core.sh）**：headless 启动应用服务层（`go run . --headless --port 7891 --no-sysproxy`）→
   代理探活 `curl -x http://127.0.0.1:7891 https://www.gstatic.com/generate_204` 期待 204
   → `curl -x … https://www.google.com` 期待 HTML → external-controller 带 secret 的 /proxies
   → SIGINT 干净退出、端口释放。
   - **注意（踩坑实录）：给 curl 指定 `-x` 时不要同时加 `--noproxy '*'`——那会把 `-x` 一并禁用导致请求直连，
     gstatic 在国内可直连会假 204，google 直连必超时。脚本先 unset 代理环境变量即可。**
   - main.go 支持 `--headless`（不开窗口，直接跑服务层）、`--port`（会话级端口覆盖，不持久化）、
     `--no-sysproxy`（本次运行禁用系统代理写入）、`--data-dir`（数据目录隔离）、
     `--controller`（external-controller 地址覆盖）——后两个供 GUI 运行期间的隔离冒烟。
3b. **v2 冒烟（scripts/smoke_v2.sh）**：GUI 占用 7890/9097 时也能跑——独立 `--data-dir` +
   `--controller 127.0.0.1:9098` + `--port 7891 --no-sysproxy`，预置本地订阅/白名单/自定义节点，
   校验 runtime.yaml 白名单规则顺序、测速 URL 为 http 版、API /proxies /rules 生效、
   系统代理前后一致；另配套 `go test ./internal/app -run TestSmokeV2` 集成测试
   （白名单热应用/节点导入删除/订阅改名改 URL/分流规则挂订阅保存与热应用
   TestSmokeV2RouteRules，动态端口不与 GUI 冲突）。
3b. **TUN 手动测试步骤**（提权流程无法自动化，需要管理员密码）：
   1. `npm run build` 安装/启动 GUI，开启 TUN → 弹管理员密码框，输入后状态栏 Tun=true，
      `~/.zprofile` 外的任意站点经 utun 出站（`curl ifconfig.me` 核对出口）；
   2. TUN 开启时切换模式/端口 → REST 重载生效（无报错、出站正常）；
   3. 关闭 TUN → 免密码退出特权内核，内嵌内核恢复（`ps aux | grep config-only` 应无残留）；
   4. 开启 TUN 时弹框点「取消」→ 回滚到内嵌内核（Tun=false），应用可正常代理；
   5. 开启 TUN 后直接退出应用 → 特权内核随 quit 标记退出，state.json tun=false。
3c. **config-only 自动化冒烟**：无需 root——`scripts/smoke_v2.sh` 第 9 段以普通用户起
   `--config-only` 内核（端口 17892/controller 19099），验证启动、REST 鉴权、quit 文件优雅退出、
   端口释放；Go 层 `internal/corectl/standalone_test.go` 用动态端口覆盖同一链路。

4. **UI**：浏览器打开 vite dev server 人工/截图检查 + ego-browser 自动化（禁 Playwright）。

## 12. 风险与对策（已评估）

| 风险 | 对策 |
|---|---|
| mihomo 库内部 API 与文档不符 | 以 vendored 源码为准（GOMODCACHE 路径见 AGENTS.md） |
| WS 自连在内核重启时断开 | 事件采集循环带自动重连（2s 间隔） |
| 订阅节点类型 mihomo 不支持（异常节点） | ParseWithBytes 失败时过滤掉无法解析的单个 proxy 重试（记录日志） |
| 7890 被占用 | Start 前探测端口，被占则报错提示换端口（不动用户 FlClash） |
