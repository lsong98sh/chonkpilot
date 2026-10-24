package pluginsdk

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config 是插件配置的只读快照（非密 + 密钥分开存放）。
type Config struct {
	values  map[string]any
	secrets map[string]any
	fields  []ConfigField
}

func loadConfig(m *Manifest) *Config {
	c := &Config{values: map[string]any{}, secrets: map[string]any{}}
	if m != nil {
		c.fields = m.Config.Fields
		for _, f := range c.fields {
			if f.Default != nil {
				c.values[f.Key] = f.Default
			}
		}
	}
	mergeJSON(c.values, os.Getenv(EnvConfig))
	mergeJSON(c.secrets, os.Getenv(EnvSecrets))
	return c
}

func mergeJSON(dst map[string]any, path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for k, v := range m {
			dst[k] = v
		}
	}
}

// Fields 返回字段声明。
func (c *Config) Fields() []ConfigField {
	if c == nil {
		return nil
	}
	return c.fields
}

// String 读字符串（密钥优先，其次非密）。
func (c *Config) String(key, def string) string {
	if c == nil {
		return def
	}
	if v, ok := c.secrets[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if v, ok := c.values[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return def
}

// Int 读整数。
func (c *Config) Int(key string, def int) int {
	if c == nil {
		return def
	}
	v, ok := c.values[key]
	if !ok {
		v = c.secrets[key]
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i
		}
	}
	return def
}

// Bool 读布尔。
func (c *Config) Bool(key string, def bool) bool {
	if c == nil {
		return def
	}
	v, ok := c.values[key]
	if !ok {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(strings.TrimSpace(b), "true") || b == "1"
	}
	return def
}

// Value 读原始值。
func (c *Config) Value(key string) any {
	if c == nil {
		return nil
	}
	if v, ok := c.values[key]; ok {
		return v
	}
	return c.secrets[key]
}

// All 返回配置快照；maskSecrets=true 时密钥字段以掩码返回（供 iframe/管理页）。
func (c *Config) All(maskSecrets bool) map[string]any {
	out := map[string]any{}
	if c == nil {
		return out
	}
	for k, v := range c.values {
		out[k] = v
	}
	for _, f := range c.fields {
		if !f.Secret {
			continue
		}
		if v, ok := c.secrets[f.Key]; ok {
			if maskSecrets {
				out[f.Key] = maskSecret(fmt.Sprint(v))
			} else {
				out[f.Key] = v
			}
		}
	}
	return out
}

func maskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
