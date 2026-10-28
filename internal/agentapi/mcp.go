package agentapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// MCP（Model Context Protocol）Streamable HTTP 传输的最小完整实现：
// 单端点 POST /mcp，JSON-RPC 2.0，响应一律 application/json（规范允许，无需 SSE）。
// 支持 initialize / ping / tools/list / tools/call，以及批量请求与通知。

const mcpLatestVersion = "2025-06-18"

var mcpSupportedVersions = map[string]bool{
	"2024-11-05": true, "2025-03-26": true, "2025-06-18": true,
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		// 不提供服务端主动推送流（GET SSE），按规范回 405。
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "读取请求失败"}})
		return
	}
	src := r.Header.Get(ClientHeader)
	if src == "" {
		src = "mcp"
	}
	out, hasReply := s.HandleRPC(body, src)
	if !hasReply {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// HandleRPC 处理一条（或一批）JSON-RPC 消息；hasReply=false 表示全是通知、无需回应。
func (s *Server) HandleRPC(body []byte, source string) (out []byte, hasReply bool) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var batch []rpcRequest
		if err := json.Unmarshal(body, &batch); err != nil {
			b, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "JSON 解析失败"}})
			return b, true
		}
		var replies []rpcResponse
		for _, req := range batch {
			if resp, ok := s.dispatch(req, source); ok {
				replies = append(replies, resp)
			}
		}
		if len(replies) == 0 {
			return nil, false
		}
		b, _ := json.Marshal(replies)
		return b, true
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		b, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "JSON 解析失败"}})
		return b, true
	}
	resp, ok := s.dispatch(req, source)
	if !ok {
		return nil, false
	}
	b, _ := json.Marshal(resp)
	return b, true
}

func (s *Server) dispatch(req rpcRequest, source string) (rpcResponse, bool) {
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if isNotification {
		return rpcResponse{}, false
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		ver := mcpLatestVersion
		if mcpSupportedVersions[p.ProtocolVersion] {
			ver = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "kunpeng-clash", "title": s.cfg.Name, "version": s.cfg.Version},
			"instructions":    s.cfg.Brief(),
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		tools := []map[string]any{}
		for _, t := range s.cfg.Registry.Tools() {
			desc := t.Desc
			if t.Danger {
				desc += "（⚠️ 影响较大，调用前请先征得用户同意）"
			}
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"title":       t.Title,
				"description": desc,
				"inputSchema": t.InputSchema(),
				"annotations": map[string]any{
					"title":           t.Title,
					"readOnlyHint":    !t.Mutates,
					"destructiveHint": t.Danger,
					"idempotentHint":  !t.Mutates,
					"openWorldHint":   false,
				},
			})
		}
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string `json:"name"`
			Arguments Args   `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{-32602, "参数格式错误: " + err.Error()}
			break
		}
		if _, ok := s.cfg.Registry.Get(p.Name); !ok {
			resp.Error = &rpcError{-32602, "未知工具: " + p.Name}
			break
		}
		res, err := s.Invoke(p.Name, p.Arguments, source)
		if err != nil {
			resp.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": "错误：" + err.Error()}},
				"isError": true,
			}
			break
		}
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": FormatResult(res)}},
			"isError": false,
		}
	default:
		resp.Error = &rpcError{-32601, "不支持的方法: " + req.Method}
	}
	return resp, true
}

// FormatResult 结果转文本：字符串原样，其余缩进 JSON（中文不转义）。
func FormatResult(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "（结果无法序列化）"
	}
	return strings.TrimRight(sb.String(), "\n")
}
