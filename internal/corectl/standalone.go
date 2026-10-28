package corectl

// RunStandalone —— config-only 独立内核模式，专供 TUN 特权子进程使用
// （GUI 进程无 root 无法创建 utun；由 osascript 提权启动本进程跑内核，
// GUI 经同一 external-controller 控制，事件流与 REST 天然兼容）。
//
// 行为约定（与 GUI 主进程完全隔离）：
//   - 跳过一切 state/订阅/系统代理逻辑：SetHomeDir(dataDir) 后直接
//     ParseWithBytes(config) 启动；
//   - 每 500ms 检查 quitFile，存在即 Shutdown 退出（GUI 可写该文件，
//     root 进程轮询自杀，关闭 TUN 无需再次提权）；
//   - 绝不写数据目录根下的任何文件（cache.db 由 mihomo 写在 HomeDir 下，
//     TUN 场景即 <dataDir>/tun/cache.db，root 属主可接受，GUI 不读写它）；
//   - 强制 no-sysproxy 语义：从不触碰系统代理设置。

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// StandaloneOptions config-only 模式参数。
type StandaloneOptions struct {
	ConfigPath     string        // 必填：内核配置文件（runtime.yaml）绝对路径
	DataDir        string        // 必填：mihomo HomeDir（TUN 场景 = <数据目录>/tun）
	ControllerAddr string        // external-controller 监听地址（如 127.0.0.1:9097）
	Secret         string        // external-controller 密钥
	QuitFile       string        // 退出标记文件：存在即优雅退出
	QuitPoll       time.Duration // quit 文件轮询周期（默认 500ms，测试可缩短）
}

// RunStandalone 以 config-only 模式运行内核直到 quit 文件出现或收到信号。
// 返回 nil 表示正常退出（quit/信号）；内核启动失败返回错误（进程以非 0 退出）。
func RunStandalone(opt StandaloneOptions) error {
	if opt.ConfigPath == "" || opt.DataDir == "" {
		return errStandalone("缺少 --config 或 --data-dir")
	}
	if opt.QuitPoll <= 0 {
		opt.QuitPoll = 500 * time.Millisecond
	}
	cfgBytes, err := os.ReadFile(opt.ConfigPath)
	if err != nil {
		return errStandalone("读取配置失败: " + err.Error())
	}
	ctl, err := New(Options{
		DataDir:        opt.DataDir,
		ControllerAddr: opt.ControllerAddr,
		Secret:         opt.Secret,
		OnLog: func(typ, payload string) {
			log.Printf("[kunpeng][standalone][%s] %s", typ, payload)
		},
	})
	if err != nil {
		return err
	}
	if err := ctl.Start(cfgBytes); err != nil {
		return err
	}
	log.Printf("[kunpeng][standalone] 内核已启动（controller=%s quit-file=%s）",
		opt.ControllerAddr, opt.QuitFile)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	tick := time.NewTicker(opt.QuitPoll)
	defer tick.Stop()

	for {
		select {
		case <-sig:
			log.Printf("[kunpeng][standalone] 收到退出信号，停止内核…")
			ctl.Shutdown()
			log.Printf("[kunpeng][standalone] 已退出")
			return nil
		case <-tick.C:
			if opt.QuitFile != "" {
				if _, serr := os.Stat(opt.QuitFile); serr == nil {
					log.Printf("[kunpeng][standalone] 检测到退出标记 %s，停止内核…", opt.QuitFile)
					ctl.Shutdown()
					log.Printf("[kunpeng][standalone] 已退出")
					return nil
				}
			}
		}
	}
}

// errStandalone 构造 config-only 模式错误。
func errStandalone(msg string) error { return &standaloneError{msg} }

type standaloneError struct{ msg string }

func (e *standaloneError) Error() string { return e.msg }
