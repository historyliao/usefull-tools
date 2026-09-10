package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const Version = 1

const (
	RestartAlways   = "always"
	RestartOnFail   = "on-failure"
	RestartNever    = "never"
	DirectionFwd    = "forward"
	DirectionRev    = "reverse"
	DefaultForward  = "R"
	DefaultBindHost = "127.0.0.1"
	DefaultSSHPort  = 22
)

var (
	namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

type Forward struct {
	Type     string `json:"type"`
	BindHost string `json:"bind_host,omitempty"`
	BindPort int    `json:"bind_port"`
	DestHost string `json:"dest_host,omitempty"`
	DestPort int    `json:"dest_port,omitempty"`
}

type Target struct {
	User      string   `json:"user"`
	Host      string   `json:"host"`
	Port      int      `json:"port,omitempty"`
	Identity  string   `json:"identity,omitempty"`
	ExtraArgs []string `json:"extra_args,omitempty"`
}

type Backoff struct {
	Initial     int `json:"initial"`
	Max         int `json:"max"`
	MaxAttempts int `json:"max_attempts"`
}

type Definition struct {
	Name      string    `json:"name"`
	Direction string    `json:"direction,omitempty"`
	Target    Target    `json:"target"`
	Forwards  []Forward `json:"forwards"`
	Restart   string    `json:"restart"`
	Backoff   Backoff   `json:"backoff"`
	Autostart bool      `json:"autostart"`
}

type Config struct {
	Version int          `json:"version"`
	Proxies []Definition `json:"proxies"`
}

func DefaultBackoff() Backoff {
	return Backoff{Initial: 1, Max: 60, MaxAttempts: 5}
}

func Load(path string) (*Config, error) {
	cfg := &Config{Version: Version}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	if cfg.Version == 0 {
		cfg.Version = Version
	}
	for i := range cfg.Proxies {
		cfg.Proxies[i] = Normalize(cfg.Proxies[i])
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	c.Version = Version
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'), 0o600)
}

func (c *Config) Find(name string) (Definition, bool) {
	for _, def := range c.Proxies {
		if def.Name == name {
			return def, true
		}
	}
	return Definition{}, false
}

func (c *Config) Index(name string) int {
	for i, def := range c.Proxies {
		if def.Name == name {
			return i
		}
	}
	return -1
}

func (c *Config) Remove(name string) bool {
	idx := c.Index(name)
	if idx < 0 {
		return false
	}
	c.Proxies = append(c.Proxies[:idx], c.Proxies[idx+1:]...)
	return true
}

func Normalize(def Definition) Definition {
	def.Name = strings.TrimSpace(def.Name)
	def.Target.Identity = strings.TrimSpace(def.Target.Identity)
	if def.Target.Port == 0 {
		def.Target.Port = DefaultSSHPort
	}
	if def.Restart == "" {
		def.Restart = RestartOnFail
	}
	def.Restart = strings.ToLower(def.Restart)
	if def.Backoff.Initial <= 0 {
		def.Backoff.Initial = 1
	}
	if def.Backoff.Max <= 0 {
		def.Backoff.Max = 60
	}
	if def.Backoff.MaxAttempts <= 0 {
		def.Backoff.MaxAttempts = 5
	}
	for i := range def.Forwards {
		def.Forwards[i] = NormalizeForward(def.Forwards[i])
	}
	if def.Direction == "" {
		def.Direction = DeriveDirection(def.Forwards)
	}
	def.Direction = strings.ToLower(strings.TrimSpace(def.Direction))
	return def
}

// DeriveDirection 按转发类型推断方向：出现 -R 即反向代理，其余（-L/-D）为正向代理。
func DeriveDirection(forwards []Forward) string {
	for _, f := range forwards {
		if f.Type == "R" {
			return DirectionRev
		}
	}
	return DirectionFwd
}

func (d Definition) DirectionLabel() string {
	if d.Direction == DirectionRev {
		return "反向"
	}
	return "正向"
}

func NormalizeForward(f Forward) Forward {
	f.Type = strings.ToUpper(strings.TrimSpace(f.Type))
	if f.Type == "" {
		f.Type = DefaultForward
	}
	if f.BindHost == "" {
		f.BindHost = DefaultBindHost
	}
	return f
}

func Validate(def Definition) error {
	if !namePattern.MatchString(def.Name) {
		return fmt.Errorf("名称 %q 非法，只允许字母、数字、点、下划线和连字符，且不能以符号开头", def.Name)
	}
	if def.Target.Host == "" {
		return fmt.Errorf("%s: 缺少目标主机", def.Name)
	}
	if !hostPattern.MatchString(def.Target.Host) {
		return fmt.Errorf("%s: 目标主机 %q 含非法字符", def.Name, def.Target.Host)
	}
	if def.Target.Port < 1 || def.Target.Port > 65535 {
		return fmt.Errorf("%s: SSH 端口 %d 超出范围", def.Name, def.Target.Port)
	}
	if len(def.Forwards) == 0 {
		return fmt.Errorf("%s: 至少需要一条转发规则", def.Name)
	}
	switch def.Restart {
	case RestartAlways, RestartOnFail, RestartNever:
	default:
		return fmt.Errorf("%s: 重启策略 %q 非法，取值为 %s/%s/%s", def.Name, def.Restart, RestartAlways, RestartOnFail, RestartNever)
	}
	switch def.Direction {
	case DirectionFwd, DirectionRev:
	default:
		return fmt.Errorf("%s: 方向 %q 非法，取值为 %s/%s", def.Name, def.Direction, DirectionFwd, DirectionRev)
	}
	for i, f := range def.Forwards {
		if def.Direction == DirectionRev && f.Type != "R" {
			return fmt.Errorf("%s: 反向代理只支持 -R 转发，第 %d 条是 -%s", def.Name, i+1, f.Type)
		}
		if def.Direction == DirectionFwd && f.Type == "R" {
			return fmt.Errorf("%s: 正向代理不支持 -R 转发，第 %d 条请拆成独立的反向代理", def.Name, i+1)
		}
		if err := validateForward(def.Name, i, f); err != nil {
			return err
		}
	}
	return nil
}

func validateForward(name string, idx int, f Forward) error {
	switch f.Type {
	case "R", "L":
		if f.BindPort < 1 || f.BindPort > 65535 {
			return fmt.Errorf("%s: 第 %d 条转发绑定端口 %d 超出范围", name, idx+1, f.BindPort)
		}
		if f.DestHost == "" || !hostPattern.MatchString(f.DestHost) {
			return fmt.Errorf("%s: 第 %d 条转发的目标主机 %q 非法", name, idx+1, f.DestHost)
		}
		if f.DestPort < 1 || f.DestPort > 65535 {
			return fmt.Errorf("%s: 第 %d 条转发的目标端口 %d 超出范围", name, idx+1, f.DestPort)
		}
	case "D":
		if f.BindPort < 1 || f.BindPort > 65535 {
			return fmt.Errorf("%s: 第 %d 条 SOCKS 端口 %d 超出范围", name, idx+1, f.BindPort)
		}
	default:
		return fmt.Errorf("%s: 第 %d 条转发类型 %q 非法，只能是 R/L/D", name, idx+1, f.Type)
	}
	return nil
}

func (f Forward) Spec() string {
	switch f.Type {
	case "D":
		return fmt.Sprintf("%s:%d", f.BindHost, f.BindPort)
	default:
		return fmt.Sprintf("%s:%d:%s:%d", f.BindHost, f.BindPort, f.DestHost, f.DestPort)
	}
}

func (f Forward) Display() string {
	return fmt.Sprintf("%s %s", f.Type, f.Spec())
}

func (d Definition) ForwardsDisplay() string {
	parts := make([]string, 0, len(d.Forwards))
	for _, f := range d.Forwards {
		parts = append(parts, f.Display())
	}
	return strings.Join(parts, ", ")
}

func (t Target) Address() string {
	host := t.Host
	if t.Port != 0 && t.Port != DefaultSSHPort {
		host = fmt.Sprintf("%s:%d", host, t.Port)
	}
	if t.User == "" {
		return host
	}
	return t.User + "@" + host
}

// Address 用于展示（含非默认端口），Destination 用于 ssh 命令行（端口由 -p 单独传）。
func (t Target) Destination() string {
	if t.User == "" {
		return t.Host
	}
	return t.User + "@" + t.Host
}

func ParseTarget(spec string) (Target, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Target{}, fmt.Errorf("目标不能为空，形如 user@host:port")
	}
	target := Target{Port: DefaultSSHPort}
	rest := spec
	if idx := strings.LastIndex(rest, "@"); idx >= 0 {
		target.User = rest[:idx]
		rest = rest[idx+1:]
	}
	if idx := strings.LastIndex(rest, ":"); idx >= 0 {
		port, err := strconv.Atoi(rest[idx+1:])
		if err != nil {
			return Target{}, fmt.Errorf("端口 %q 不是数字", rest[idx+1:])
		}
		target.Port = port
		rest = rest[:idx]
	}
	target.Host = rest
	if target.Host == "" {
		return Target{}, fmt.Errorf("目标主机为空")
	}
	return target, nil
}

func ParseForward(kind, spec string) (Forward, error) {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	if kind == "" {
		kind = DefaultForward
	}
	f := Forward{Type: kind, BindHost: DefaultBindHost}
	parts := strings.Split(strings.TrimSpace(spec), ":")
	switch kind {
	case "R", "L":
		if len(parts) != 4 {
			return Forward{}, fmt.Errorf("%s 转发需要 bind_host:bind_port:dest_host:dest_port，收到 %q", kind, spec)
		}
		if parts[0] != "" {
			f.BindHost = parts[0]
		}
		bindPort, err := strconv.Atoi(parts[1])
		if err != nil {
			return Forward{}, fmt.Errorf("绑定端口 %q 不是数字", parts[1])
		}
		destPort, err := strconv.Atoi(parts[3])
		if err != nil {
			return Forward{}, fmt.Errorf("目标端口 %q 不是数字", parts[3])
		}
		f.BindPort, f.DestHost, f.DestPort = bindPort, parts[2], destPort
	case "D":
		if len(parts) != 2 {
			return Forward{}, fmt.Errorf("D 转发需要 bind_host:bind_port，收到 %q", spec)
		}
		if parts[0] != "" {
			f.BindHost = parts[0]
		}
		bindPort, err := strconv.Atoi(parts[1])
		if err != nil {
			return Forward{}, fmt.Errorf("SOCKS 端口 %q 不是数字", parts[1])
		}
		f.BindPort = bindPort
	default:
		return Forward{}, fmt.Errorf("转发类型 %q 非法，只能是 R/L/D", kind)
	}
	return f, nil
}

func ParseForwardExpr(expr string) (Forward, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return Forward{}, fmt.Errorf("转发规则为空")
	}
	fields := strings.Fields(expr)
	if len(fields) == 1 {
		return ParseForward("", fields[0])
	}
	if len(fields) != 2 {
		return Forward{}, fmt.Errorf("转发规则 %q 非法，形如 \"R 127.0.0.1:9000:192.168.179.3:9000\"", expr)
	}
	return ParseForward(fields[0], fields[1])
}

func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
