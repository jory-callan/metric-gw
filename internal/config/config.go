package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 全局配置
type Config struct {
	Server   ServerConfig    `yaml:"server"`
	Auth     AuthConfig      `yaml:"auth"`
	Buffer   BufferConfig    `yaml:"buffer"`
	Flush    FlushConfig     `yaml:"flush"`
	Backends []BackendConfig `yaml:"backends"`
}

// ServerConfig HTTP 服务器配置
type ServerConfig struct {
	Listen      string `yaml:"listen"`
	MaxBodySize string `yaml:"max_body_size"`
}

// AuthConfig 认证配置
type AuthConfig struct {
	Mode     string `yaml:"mode"` // apikey | basic | none
	APIKey   string `yaml:"api_key"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// BufferConfig 缓冲配置
type BufferConfig struct {
	Memory MemoryBufferConfig `yaml:"memory"`
	Disk   DiskBufferConfig   `yaml:"disk"`
}

// MemoryBufferConfig 内存队列配置
type MemoryBufferConfig struct {
	MaxItems int `yaml:"max_items"`
}

// DiskBufferConfig 磁盘队列配置
type DiskBufferConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
	MaxSize string `yaml:"max_size"`
}

// FlushConfig flush 配置
type FlushConfig struct {
	BatchSize int          `yaml:"batch_size"`
	Interval  string       `yaml:"interval"`
	Timeout   string       `yaml:"timeout"`
	Retry     RetryConfig  `yaml:"retry"`
}

// RetryConfig 重试配置
type RetryConfig struct {
	MaxAttempts  int    `yaml:"max_attempts"` // 0 = infinite
	Backoff      string `yaml:"backoff"`      // exponential | fixed
	InitialDelay string `yaml:"initial_delay"`
	MaxDelay     string `yaml:"max_delay"`
}

// BackendConfig 后端配置
type BackendConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // vm_import | remote_write
	URL  string `yaml:"url"`
}

// Load 从文件加载配置并校验
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.setDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":9201"
	}
	if c.Server.MaxBodySize == "" {
		c.Server.MaxBodySize = "10MB"
	}
	if c.Auth.Mode == "" {
		c.Auth.Mode = "none"
	}
	if c.Buffer.Memory.MaxItems == 0 {
		c.Buffer.Memory.MaxItems = 100000
	}
	if c.Buffer.Disk.Path == "" {
		c.Buffer.Disk.Path = "/data/metric-gw"
	}
	if c.Buffer.Disk.MaxSize == "" {
		c.Buffer.Disk.MaxSize = "1GB"
	}
	if c.Flush.BatchSize == 0 {
		c.Flush.BatchSize = 5000
	}
	if c.Flush.Interval == "" {
		c.Flush.Interval = "1s"
	}
	if c.Flush.Timeout == "" {
		c.Flush.Timeout = "10s"
	}
	if c.Flush.Retry.Backoff == "" {
		c.Flush.Retry.Backoff = "exponential"
	}
	if c.Flush.Retry.InitialDelay == "" {
		c.Flush.Retry.InitialDelay = "1s"
	}
	if c.Flush.Retry.MaxDelay == "" {
		c.Flush.Retry.MaxDelay = "30s"
	}
}

func (c *Config) validate() error {
	if len(c.Backends) == 0 {
		return fmt.Errorf("at least one backend is required")
	}

	names := make(map[string]bool)
	for i, b := range c.Backends {
		if b.Name == "" {
			return fmt.Errorf("backend[%d]: name is required", i)
		}
		if names[b.Name] {
			return fmt.Errorf("backend[%d]: duplicate name %q", i, b.Name)
		}
		names[b.Name] = true

		switch b.Type {
		case "vm_import", "remote_write":
		default:
			return fmt.Errorf("backend %q: invalid type %q (must be vm_import or remote_write)", b.Name, b.Type)
		}

		if b.URL == "" {
			return fmt.Errorf("backend %q: url is required", b.Name)
		}
	}

	switch c.Auth.Mode {
	case "apikey", "basic", "none":
	default:
		return fmt.Errorf("auth.mode must be apikey, basic, or none")
	}

	if c.Auth.Mode == "apikey" && c.Auth.APIKey == "" {
		return fmt.Errorf("auth.api_key is required when mode=apikey")
	}
	if c.Auth.Mode == "basic" {
		if c.Auth.Username == "" || c.Auth.Password == "" {
			return fmt.Errorf("auth.username and auth.password are required when mode=basic")
		}
	}

	return nil
}

// ParseSize 解析带单位的大小字符串 (如 "10MB", "1GB", "512KB")
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}

	re := regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(B|KB|MB|GB|TB)?$`)
	matches := re.FindStringSubmatch(strings.ToUpper(s))
	if matches == nil {
		return 0, fmt.Errorf("invalid size format: %q", s)
	}

	num, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size number: %q", s)
	}

	var multiplier float64 = 1
	switch matches[2] {
	case "", "B":
		multiplier = 1
	case "KB":
		multiplier = 1024
	case "MB":
		multiplier = 1024 * 1024
	case "GB":
		multiplier = 1024 * 1024 * 1024
	case "TB":
		multiplier = 1024 * 1024 * 1024 * 1024
	}

	return int64(num * multiplier), nil
}

// ParseDuration 解析时间间隔字符串
func ParseDuration(s string) (time.Duration, error) {
	return time.ParseDuration(s)
}

// MaxBodySizeBytes 返回 max_body_size 的字节数
func (c *Config) MaxBodySizeBytes() (int64, error) {
	return ParseSize(c.Server.MaxBodySize)
}

// FlushIntervalDuration 返回 flush interval
func (c *Config) FlushIntervalDuration() (time.Duration, error) {
	return ParseDuration(c.Flush.Interval)
}

// FlushTimeoutDuration 返回 flush timeout
func (c *Config) FlushTimeoutDuration() (time.Duration, error) {
	return ParseDuration(c.Flush.Timeout)
}

// DiskMaxSizeBytes 返回磁盘队列最大字节数
func (c *Config) DiskMaxSizeBytes() (int64, error) {
	return ParseSize(c.Buffer.Disk.MaxSize)
}

// RetryInitialDelay 返回初始重试延迟
func (c *Config) RetryInitialDelay() (time.Duration, error) {
	return ParseDuration(c.Flush.Retry.InitialDelay)
}

// RetryMaxDelay 返回最大重试延迟
func (c *Config) RetryMaxDelay() (time.Duration, error) {
	return ParseDuration(c.Flush.Retry.MaxDelay)
}
