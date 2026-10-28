package agentapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// CallRecord 一次工具调用的记录（UI 调用记录与事件推送用）。
type CallRecord struct {
	Tool       string `json:"tool"`
	Title      string `json:"title"`
	Source     string `json:"source"` // mcp / http / cli / mcp-stdio
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	At         string `json:"at"` // RFC3339
	DurationMs int64  `json:"durationMs"`
	Mutates    bool   `json:"mutates"`
}

// Config 网关配置。函数字段在每次请求时求值，令牌重置/教程变化即时生效。
type Config struct {
	Registry *Registry
	Name     string
	Version  string
	Token    func() string
	Guide    func() string // 完整教程（Markdown，免鉴权：不含令牌）
	Skill    func() string // SKILL.md（免鉴权：不含令牌）
	Brief    func() string // MCP initialize.instructions
	OnCall   func(CallRecord)
}

// Server 本机智能体网关。
type Server struct {
	cfg  Config
	srv  *http.Server
	addr string
}

// ClientHeader 桥接客户端（ctl / mcp stdio）标识自身来源的请求头。
const ClientHeader = "X-Kunpeng-Client"

// Start 依次尝试 ports，监听第一个可用的 127.0.0.1 端口。
func Start(cfg Config, ports []int) (*Server, error) {
	var ln net.Listener
	var err error
	for _, p := range ports {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			break
		}
	}
	if ln == nil {
		return nil, fmt.Errorf("智能体网关端口均被占用: %w", err)
	}
	s := &Server{cfg: cfg, addr: ln.Addr().String()}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/v1/ping", s.handlePing)
	mux.HandleFunc("/v1/guide", s.handleGuide)
	mux.HandleFunc("/v1/skill", s.handleSkill)
	mux.HandleFunc("/v1/tools", s.auth(s.handleTools))
	mux.HandleFunc("/v1/call/", s.auth(s.handleCall))
	mux.HandleFunc("/mcp", s.auth(s.handleMCP))
	s.srv = &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[kunpeng][agent] 网关退出: %v", err)
		}
	}()
	return s, nil
}

// Addr 实际监听地址（host:port）。
func (s *Server) Addr() string { return s.addr }

// Close 关闭网关。
func (s *Server) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
}

// guard 拒绝浏览器跨站与 DNS 重绑定：只接受 Host 为本机地址、且不带网页 Origin 的请求。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "仅允许本机访问"})
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "null" {
			writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "不接受网页跨站请求"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if got == "" {
			got = r.Header.Get("X-Kunpeng-Token")
		}
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		want := s.cfg.Token()
		if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"ok":    false,
				"error": "令牌无效：请在请求头带 Authorization: Bearer <令牌>（令牌见鲲鹏 Clash →「智能体」页）",
			})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "未知路径；教程见 GET /v1/guide"})
		return
	}
	s.handleGuide(w, r)
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "app": s.cfg.Name, "version": s.cfg.Version})
}

func (s *Server) handleGuide(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, s.cfg.Guide())
}

func (s *Server) handleSkill(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, s.cfg.Skill())
}

func (s *Server) handleTools(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		Tool
		InputSchema map[string]any `json:"inputSchema"`
	}
	out := []item{}
	for _, t := range s.cfg.Registry.Tools() {
		out = append(out, item{Tool: t, InputSchema: t.InputSchema()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tools": out})
}

func (s *Server) handleCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "请用 POST"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/v1/call/")
	args := Args{}
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, &args); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请求体须是 JSON 对象: " + err.Error()})
				return
			}
		}
	} else {
		for k, v := range r.URL.Query() {
			if k != "token" && len(v) > 0 {
				args[k] = v[0]
			}
		}
	}
	src := r.Header.Get(ClientHeader)
	if src == "" {
		src = "http"
	}
	res, err := s.Invoke(name, args, src)
	if err != nil {
		status := http.StatusOK
		if errors.Is(err, errUnknownTool) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

var errUnknownTool = errors.New("未知工具")

// Invoke 校验参数并执行工具，记录调用（panic 也转为错误，不拖垮应用）。
func (s *Server) Invoke(name string, args Args, source string) (res any, err error) {
	t, ok := s.cfg.Registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w %q；GET /v1/tools 查看全部工具", errUnknownTool, name)
	}
	if args == nil {
		args = Args{}
	}
	start := time.Now()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("工具执行异常: %v", p)
		}
		if s.cfg.OnCall != nil {
			rec := CallRecord{
				Tool: t.Name, Title: t.Title, Source: source, OK: err == nil,
				At: start.Format(time.RFC3339), DurationMs: time.Since(start).Milliseconds(), Mutates: t.Mutates,
			}
			if err != nil {
				rec.Error = err.Error()
			}
			s.cfg.OnCall(rec)
		}
	}()
	if err := t.validate(args); err != nil {
		return nil, err
	}
	return t.Handler(args)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
