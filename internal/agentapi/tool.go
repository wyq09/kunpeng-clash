// Package agentapi 智能体接入网关的传输层：一份工具注册表，经三种通道暴露——
// HTTP（/v1/call/<tool>）、MCP（Streamable HTTP /mcp 与 stdio 桥）、命令行（ctl）。
// 工具语义由 internal/app 注册；本包不认识任何业务。
package agentapi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Param 工具参数。Type 取 JSON Schema 基础类型：string / integer / boolean / array（字符串数组）。
type Param struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Desc     string   `json:"description"`
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

// Tool 一个对智能体开放的能力。
type Tool struct {
	Name    string  `json:"name"`
	Title   string  `json:"title"`
	Group   string  `json:"group"`
	Desc    string  `json:"description"`
	Params  []Param `json:"params"`
	Mutates bool    `json:"mutates"` // 会改变应用状态（UI 据此提示并刷新）
	Danger  bool    `json:"danger"`  // 可能断网/删数据/弹系统授权框，智能体应先征得用户同意

	Handler func(a Args) (any, error) `json:"-"`
}

// InputSchema 生成 MCP / OpenAPI 通用的 JSON Schema。
func (t Tool) InputSchema() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range t.Params {
		s := map[string]any{"type": p.Type, "description": p.Desc}
		if p.Type == "array" {
			s["items"] = map[string]any{"type": "string"}
		}
		if len(p.Enum) > 0 {
			s["enum"] = p.Enum
		}
		props[p.Name] = s
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// Args 工具调用参数。数值/布尔/数组都容忍字符串形态（命令行 key=value 传进来的全是字符串）。
type Args map[string]any

// Has 参数是否出现（区分「没传」与「传了零值」）。
func (a Args) Has(k string) bool {
	v, ok := a[k]
	return ok && v != nil
}

// Str 字符串参数（去首尾空白）。
func (a Args) Str(k string) string {
	switch v := a[k].(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// Int 整数参数；缺省返回 def。
func (a Args) Int(k string, def int) (int, error) {
	switch v := a[k].(type) {
	case nil:
		return def, nil
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case json.Number:
		n, err := v.Int64()
		return int(n), err
	case string:
		if strings.TrimSpace(v) == "" {
			return def, nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("参数 %s 需要整数，收到 %q", k, v)
		}
		return n, nil
	}
	return 0, fmt.Errorf("参数 %s 需要整数", k)
}

// Bool 布尔参数；缺省返回 def。接受 true/false/1/0/on/off/yes/no/开/关。
func (a Args) Bool(k string, def bool) (bool, error) {
	switch v := a[k].(type) {
	case nil:
		return def, nil
	case bool:
		return v, nil
	case float64:
		return v != 0, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "":
			return def, nil
		case "true", "1", "on", "yes", "y", "开", "开启", "打开":
			return true, nil
		case "false", "0", "off", "no", "n", "关", "关闭":
			return false, nil
		}
		return false, fmt.Errorf("参数 %s 需要 true/false，收到 %q", k, v)
	}
	return false, fmt.Errorf("参数 %s 需要 true/false", k)
}

// List 字符串数组参数：接受 JSON 数组，或以换行/逗号/空格分隔的字符串。
func (a Args) List(k string) []string {
	var out []string
	push := func(s string) {
		for _, f := range strings.FieldsFunc(s, func(r rune) bool {
			return r == '\n' || r == ',' || r == '，' || r == ' ' || r == '\t' || r == ';'
		}) {
			if f = strings.TrimSpace(f); f != "" {
				out = append(out, f)
			}
		}
	}
	switch v := a[k].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	case []string:
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case string:
		push(v)
	}
	return out
}

// Registry 工具注册表（按注册顺序列出）。
type Registry struct {
	tools []Tool
	index map[string]int
}

// NewRegistry 空注册表。
func NewRegistry() *Registry { return &Registry{index: map[string]int{}} }

// Add 注册工具（同名覆盖）。
func (r *Registry) Add(t Tool) {
	if i, ok := r.index[t.Name]; ok {
		r.tools[i] = t
		return
	}
	r.index[t.Name] = len(r.tools)
	r.tools = append(r.tools, t)
}

// Tools 全部工具。
func (r *Registry) Tools() []Tool { return r.tools }

// Get 按名取工具（容忍 - 与 _ 混用）。
func (r *Registry) Get(name string) (Tool, bool) {
	name = strings.ReplaceAll(strings.TrimSpace(name), "-", "_")
	i, ok := r.index[name]
	if !ok {
		return Tool{}, false
	}
	return r.tools[i], true
}

// validate 检查必填参数与未知参数，给出可操作的报错（告诉智能体正确的参数名）。
func (t Tool) validate(a Args) error {
	known := map[string]bool{}
	for _, p := range t.Params {
		known[p.Name] = true
	}
	for k := range a {
		if !known[k] {
			names := make([]string, 0, len(t.Params))
			for _, p := range t.Params {
				names = append(names, p.Name)
			}
			if len(names) == 0 {
				return fmt.Errorf("工具 %s 不接受参数，收到 %s", t.Name, k)
			}
			return fmt.Errorf("未知参数 %s；工具 %s 可用参数：%s", k, t.Name, strings.Join(names, ", "))
		}
	}
	for _, p := range t.Params {
		if p.Required && !a.Has(p.Name) {
			return fmt.Errorf("缺少必填参数 %s（%s）", p.Name, p.Desc)
		}
		if len(p.Enum) > 0 && a.Has(p.Name) {
			v := a.Str(p.Name)
			okv := false
			for _, e := range p.Enum {
				if e == v {
					okv = true
				}
			}
			if !okv {
				return fmt.Errorf("参数 %s 取值应为 %s，收到 %q", p.Name, strings.Join(p.Enum, "/"), v)
			}
		}
	}
	return nil
}
