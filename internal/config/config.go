package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// RelayConfig 描述 relay 进程自身的监听地址、路由规则、目标站点和 webhook 入口。
type RelayConfig struct {
	ServiceName    string          `yaml:"service_name"`
	HTTPListen     string          `yaml:"http_listen"`
	GRPCListen     string          `yaml:"grpc_listen"`
	DataFile       string          `yaml:"data_file"`
	SourceRegistry string          `yaml:"source_registry"`
	LogLevel       string          `yaml:"log_level"`
	LogFormat      string          `yaml:"log_format"`
	Webhook        WebhookConfig   `yaml:"webhook"`
	Webhooks       []WebhookConfig `yaml:"webhooks"`
	Routes         []RouteConfig   `yaml:"routes"`
	Targets        []TargetConfig  `yaml:"targets"`
}

// WebhookConfig 描述一个 Harbor webhook 入口。
// 一个 relay 可以同时承接多个 path，也可以给不同项目分配不同鉴权头。
type WebhookConfig struct {
	Name           string `yaml:"name"`
	Path           string `yaml:"path"`
	Authorization  string `yaml:"authorization"`
	SourceRegistry string `yaml:"source_registry"`
	Enabled        *bool  `yaml:"enabled"`
}

// RouteConfig 把 Harbor 仓库模式映射为逻辑 channel，再映射到一个或多个 target site。
// channel 是调度维度，远端 agent 只订阅自己关心的 channel。
type RouteConfig struct {
	Name               string   `yaml:"name"`
	Channel            string   `yaml:"channel"`
	Enabled            *bool    `yaml:"enabled"`
	WebhookNames       []string `yaml:"webhook_names"`
	RepositoryPatterns []string `yaml:"repository_patterns"`
	TargetSites        []string `yaml:"target_sites"`
}

// TargetConfig 描述一个远端站点的目标仓库信息和回调配置。
type TargetConfig struct {
	Name               string   `yaml:"name"`
	SiteName           string   `yaml:"site_name"`
	Enabled            *bool    `yaml:"enabled"`
	TargetRegistry     string   `yaml:"target_registry"`
	TargetProject      string   `yaml:"target_project"`
	RepositoryPrefix   string   `yaml:"repository_prefix"`
	RepositoryPatterns []string `yaml:"repository_patterns"`
	CallbackURL        string   `yaml:"callback_url"`
	CallbackToken      string   `yaml:"callback_token"`
}

// AgentConfig 描述远端 agent 如何连接 relay、以及如何登录源/目标仓库。
type AgentConfig struct {
	AgentID            string        `yaml:"agent_id"`
	SiteName           string        `yaml:"site_name"`
	Channels           []string      `yaml:"channels"`
	Version            string        `yaml:"version"`
	LogLevel           string        `yaml:"log_level"`
	LogFormat          string        `yaml:"log_format"`
	RelayAddress       string        `yaml:"relay_address"`
	RelayServerName    string        `yaml:"relay_server_name"`
	SourceRegistry     string        `yaml:"source_registry"`
	SourceUsername     string        `yaml:"source_username"`
	SourcePassword     string        `yaml:"source_password"`
	TargetRegistry     string        `yaml:"target_registry"`
	TargetUsername     string        `yaml:"target_username"`
	TargetPassword     string        `yaml:"target_password"`
	DockerBinary       string        `yaml:"docker_binary"`
	HeartbeatInterval  time.Duration `yaml:"heartbeat_interval"`
	ReconnectInterval  time.Duration `yaml:"reconnect_interval"`
	MaxSessionAge      time.Duration `yaml:"max_session_age"`
	CleanupLocalImages bool          `yaml:"cleanup_local_images"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify"`
}

// LoadRelayConfig 负责加载 relay 配置，并补齐向后兼容的默认值。
func LoadRelayConfig(path string) (RelayConfig, error) {
	var cfg RelayConfig
	if err := load(path, &cfg); err != nil {
		return cfg, err
	}

	if cfg.ServiceName == "" {
		cfg.ServiceName = "harbor-relay"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.LogFormat == "" {
		cfg.LogFormat = "text"
	}
	if cfg.HTTPListen == "" {
		cfg.HTTPListen = ":18080"
	}
	if cfg.GRPCListen == "" {
		cfg.GRPCListen = ":19090"
	}
	if cfg.DataFile == "" {
		cfg.DataFile = "./data/relay-state.json"
	}
	if cfg.Webhook.Path == "" {
		cfg.Webhook.Path = "/api/v1/harbor/webhook"
	}
	if len(cfg.Webhooks) == 0 {
		cfg.Webhooks = []WebhookConfig{cfg.Webhook}
	}
	for i := range cfg.Webhooks {
		if cfg.Webhooks[i].Name == "" {
			cfg.Webhooks[i].Name = fmt.Sprintf("webhook-%d", i+1)
		}
		if cfg.Webhooks[i].Path == "" {
			cfg.Webhooks[i].Path = "/api/v1/harbor/webhook"
		}
		if cfg.Webhooks[i].SourceRegistry == "" {
			cfg.Webhooks[i].SourceRegistry = cfg.SourceRegistry
		}
	}
	for i := range cfg.Routes {
		if cfg.Routes[i].Name == "" {
			cfg.Routes[i].Name = fmt.Sprintf("route-%d", i+1)
		}
		if cfg.Routes[i].Channel == "" {
			cfg.Routes[i].Channel = cfg.Routes[i].Name
		}
	}
	for i := range cfg.Targets {
		if cfg.Targets[i].SiteName == "" {
			cfg.Targets[i].SiteName = cfg.Targets[i].Name
		}
	}
	return cfg, nil
}

// IsEnabled 让 enabled 为空时也按 true 处理，降低配置门槛。
func (w WebhookConfig) IsEnabled() bool {
	return w.Enabled == nil || *w.Enabled
}

func (r RouteConfig) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// AllowsWebhook 用于控制某条 route 是否允许某个 webhook 入口命中。
// 如果 webhook_names 为空，表示该 route 对所有 webhook 入口都生效。
func (r RouteConfig) AllowsWebhook(webhookName string) bool {
	if len(r.WebhookNames) == 0 {
		return true
	}
	for _, name := range r.WebhookNames {
		if name == webhookName {
			return true
		}
	}
	return false
}

func (t TargetConfig) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// LoadAgentConfig 负责加载 agent 配置，并补上运行期默认值。
func LoadAgentConfig(path string) (AgentConfig, error) {
	var cfg AgentConfig
	if err := load(path, &cfg); err != nil {
		return cfg, err
	}

	if cfg.AgentID == "" {
		return cfg, fmt.Errorf("agent_id is required")
	}
	if cfg.SiteName == "" {
		return cfg, fmt.Errorf("site_name is required")
	}
	if cfg.RelayAddress == "" {
		return cfg, fmt.Errorf("relay_address is required")
	}
	if cfg.SourceRegistry == "" {
		return cfg, fmt.Errorf("source_registry is required")
	}
	if cfg.TargetRegistry == "" {
		return cfg, fmt.Errorf("target_registry is required")
	}
	if cfg.DockerBinary == "" {
		cfg.DockerBinary = "docker"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.LogFormat == "" {
		cfg.LogFormat = "text"
	}
	if len(cfg.Channels) == 0 {
		cfg.Channels = []string{"*"}
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 10 * time.Second
	}
	if cfg.ReconnectInterval == 0 {
		cfg.ReconnectInterval = 5 * time.Second
	}
	if cfg.MaxSessionAge == 0 {
		cfg.MaxSessionAge = 30 * time.Minute
	}
	return cfg, nil
}

func load(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}
