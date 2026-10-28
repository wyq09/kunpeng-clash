package agentapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Endpoint 运行中应用写给桥接客户端的发现文件（<数据目录>/agent.json，0600）。
// ctl / mcp stdio 读它拿到地址与令牌，用户与智能体都不需要手抄令牌。
type Endpoint struct {
	BaseURL string `json:"baseUrl"`
	MCPURL  string `json:"mcpUrl"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// EndpointFile 发现文件名。
const EndpointFile = "agent.json"

// WriteEndpoint 写发现文件（仅属主可读）。
func WriteEndpoint(dataDir string, e Endpoint) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dataDir, EndpointFile)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// RemoveEndpoint 网关关闭时删除发现文件。
func RemoveEndpoint(dataDir string) { _ = os.Remove(filepath.Join(dataDir, EndpointFile)) }

var errAppDown = errors.New("鲲鹏 Clash 未运行，或「智能体」页的接入开关已关闭——请先打开应用：open -a KunpengClash")

func readEndpoint(dataDir string) (Endpoint, error) {
	var e Endpoint
	b, err := os.ReadFile(filepath.Join(dataDir, EndpointFile))
	if err != nil {
		return e, errAppDown
	}
	if err := json.Unmarshal(b, &e); err != nil || e.BaseURL == "" {
		return e, errAppDown
	}
	return e, nil
}

var bridgeClient = &http.Client{Timeout: 120 * time.Second, Transport: &http.Transport{Proxy: nil}}

func post(e Endpoint, url, client string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(ClientHeader, client)
	resp, err := bridgeClient.Do(req)
	if err != nil {
		return nil, errAppDown
	}
	return resp, nil
}

// RunStdioMCP MCP stdio 传输桥：逐行读 JSON-RPC → 转发到应用 /mcp → 回写一行。
// 每条消息重新读取发现文件，应用重启或令牌重置后无需重连。
func RunStdioMCP(dataDir string, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		reply := stdioForward(dataDir, line)
		if reply == nil {
			continue
		}
		_, _ = w.Write(bytes.TrimSpace(reply))
		_ = w.WriteByte('\n')
		_ = w.Flush()
	}
	return sc.Err()
}

func stdioForward(dataDir string, line []byte) []byte {
	var probe struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(line, &probe)
	fail := func(err error) []byte {
		if len(probe.ID) == 0 || string(probe.ID) == "null" {
			return nil
		}
		b, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: probe.ID, Error: &rpcError{-32000, err.Error()}})
		return b
	}
	e, err := readEndpoint(dataDir)
	if err != nil {
		return fail(err)
	}
	resp, err := post(e, e.MCPURL, "mcp-stdio", line)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			if s, _ := m["error"].(string); s != "" {
				return fail(errors.New(s))
			}
		}
		return fail(fmt.Errorf("网关返回 HTTP %d", resp.StatusCode))
	}
	return body
}

// RunCLI 命令行入口：ctl [工具名] [key=value ...] | ctl 工具名 '{"json":"参数"}' | ctl guide | ctl tools
// 返回进程退出码。
func RunCLI(dataDir string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, cliUsage)
		return listTools(dataDir, stdout, stderr)
	}
	e, err := readEndpoint(dataDir)
	if err != nil {
		fmt.Fprintln(stderr, "错误："+err.Error())
		return 2
	}
	switch args[0] {
	case "guide":
		return getText(e, "/v1/guide", stdout, stderr)
	case "skill":
		return getText(e, "/v1/skill", stdout, stderr)
	case "tools", "list":
		return listTools(dataDir, stdout, stderr)
	}
	name := args[0]
	callArgs := Args{}
	rest := args[1:]
	if len(rest) == 1 && strings.HasPrefix(strings.TrimSpace(rest[0]), "{") {
		if err := json.Unmarshal([]byte(rest[0]), &callArgs); err != nil {
			fmt.Fprintln(stderr, "错误：JSON 参数解析失败: "+err.Error())
			return 2
		}
	} else {
		for _, kv := range rest {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				fmt.Fprintf(stderr, "错误：参数需写成 key=value，收到 %q\n", kv)
				return 2
			}
			k = strings.TrimLeft(k, "-")
			if prev, dup := callArgs[k].(string); dup {
				v = prev + "\n" + v // 同名参数重复出现 → 合并为列表
			}
			callArgs[k] = v
		}
	}
	body, _ := json.Marshal(callArgs)
	resp, err := post(e, e.BaseURL+"/v1/call/"+name, "cli", body)
	if err != nil {
		fmt.Fprintln(stderr, "错误："+err.Error())
		return 2
	}
	defer resp.Body.Close()
	var out struct {
		OK     bool   `json:"ok"`
		Result any    `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fmt.Fprintf(stderr, "错误：网关返回无法解析（HTTP %d）\n", resp.StatusCode)
		return 1
	}
	if !out.OK {
		fmt.Fprintln(stderr, "错误："+out.Error)
		return 1
	}
	fmt.Fprintln(stdout, FormatResult(out.Result))
	return 0
}

func getText(e Endpoint, path string, stdout, stderr io.Writer) int {
	resp, err := bridgeClient.Get(e.BaseURL + path)
	if err != nil {
		fmt.Fprintln(stderr, "错误："+errAppDown.Error())
		return 2
	}
	defer resp.Body.Close()
	_, _ = io.Copy(stdout, resp.Body)
	return 0
}

func listTools(dataDir string, stdout, stderr io.Writer) int {
	e, err := readEndpoint(dataDir)
	if err != nil {
		fmt.Fprintln(stderr, "错误："+err.Error())
		return 2
	}
	req, _ := http.NewRequest(http.MethodGet, e.BaseURL+"/v1/tools", nil)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	resp, err := bridgeClient.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "错误："+errAppDown.Error())
		return 2
	}
	defer resp.Body.Close()
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		fmt.Fprintln(stderr, "错误：工具清单解析失败")
		return 1
	}
	groups := map[string][]Tool{}
	order := []string{}
	for _, t := range out.Tools {
		if _, ok := groups[t.Group]; !ok {
			order = append(order, t.Group)
		}
		groups[t.Group] = append(groups[t.Group], t)
	}
	for _, g := range order {
		fmt.Fprintf(stdout, "\n【%s】\n", g)
		for _, t := range groups[g] {
			ps := []string{}
			for _, p := range t.Params {
				n := p.Name + "=" + p.Type
				if !p.Required {
					n = "[" + n + "]"
				}
				ps = append(ps, n)
			}
			fmt.Fprintf(stdout, "  %-24s %s  %s\n", t.Name, t.Title, strings.Join(ps, " "))
		}
	}
	return 0
}

const cliUsage = `鲲鹏 Clash 命令行（需应用在运行）

用法：
  KunpengClash ctl <工具名> [key=value ...]     调用工具，例如：ctl select_node node=香港
  KunpengClash ctl <工具名> '{"key":"value"}'   用 JSON 传参
  KunpengClash ctl tools                        列出全部工具
  KunpengClash ctl guide                        打印完整智能体教程
  KunpengClash mcp                              以 stdio 方式运行 MCP 服务（给智能体配置用）

数组参数可用逗号分隔：domains=chatgpt.com,openai.com
`
