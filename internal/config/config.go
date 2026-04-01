package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RelayConfig describes the relay process itself: listeners, routes,
// target sites, notification channels, and webhook entry points.
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

// WebhookConfig describes one Harbor webhook entry point.
// A single relay can expose multiple paths with different auth headers.
type WebhookConfig struct {
	Name           string `yaml:"name"`
	Path           string `yaml:"path"`
	Authorization  string `yaml:"authorization"`
	SourceRegistry string `yaml:"source_registry"`
	Enabled        *bool  `yaml:"enabled"`
}

// RouteConfig maps Harbor repositories to a logical channel,
// then maps that channel to one or more target sites.
type RouteConfig struct {
	Name               string   `yaml:"name"`
	Channel            string   `yaml:"channel"`
	Enabled            *bool    `yaml:"enabled"`
	WebhookNames       []string `yaml:"webhook_names"`
	RepositoryPatterns []string `yaml:"repository_patterns"`
	TargetSites        []string `yaml:"target_sites"`
}

// TargetConfig describes the destination site and the post-sync callback/notification settings.
type TargetConfig struct {
	Name               string               `yaml:"name"`
	SiteName           string               `yaml:"site_name"`
	Enabled            *bool                `yaml:"enabled"`
	TargetRegistry     string               `yaml:"target_registry"`
	TargetProject      string               `yaml:"target_project"`
	RepositoryPrefix   string               `yaml:"repository_prefix"`
	RepositoryPatterns []string             `yaml:"repository_patterns"`
	CallbackEnabled    *bool                `yaml:"callback_enabled"`
	CallbackURL        string               `yaml:"callback_url"`
	CallbackToken      string               `yaml:"callback_token"`
	Notifications      []NotificationConfig `yaml:"notifications"`
}

// NotificationConfig describes one outbound notification channel.
// The current implementation focuses on OneMsg robots, but the model is
// intentionally generic so more gateways can be added later.
type NotificationConfig struct {
	Name          string        `yaml:"name"`
	Type          string        `yaml:"type"`
	Enabled       *bool         `yaml:"enabled"`
	Endpoint      string        `yaml:"endpoint"`
	RobotKey      string        `yaml:"robot_key"`
	Events        []string      `yaml:"events"`
	Timeout       time.Duration `yaml:"timeout"`
	TitlePrefix   string        `yaml:"title_prefix"`
	MinInterval   time.Duration `yaml:"min_interval"`
	RetryInterval time.Duration `yaml:"retry_interval"`
	MaxAttempts   int           `yaml:"max_attempts"`
}

// AgentConfig describes how one remote agent connects to relay
// and how it logs in to source/target registries.
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
	DockerConfigDir    string        `yaml:"docker_config_dir"`
	HeartbeatInterval  time.Duration `yaml:"heartbeat_interval"`
	ReconnectInterval  time.Duration `yaml:"reconnect_interval"`
	MaxSessionAge      time.Duration `yaml:"max_session_age"`
	CleanupLocalImages bool          `yaml:"cleanup_local_images"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify"`
}

// LoadRelayConfig loads relay.yaml and fills backward-compatible defaults.
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
		cfg.DataFile = "/var/lib/harbor-relay/relay-state.json"
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
		for j := range cfg.Targets[i].Notifications {
			notification := &cfg.Targets[i].Notifications[j]
			if notification.Name == "" {
				notification.Name = fmt.Sprintf("%s-notify-%d", cfg.Targets[i].SiteName, j+1)
			}
			if notification.Type == "" {
				notification.Type = "onemsg_robot"
			}
			if notification.Timeout == 0 {
				notification.Timeout = 10 * time.Second
			}
			if notification.MinInterval == 0 {
				notification.MinInterval = time.Minute
			}
			if notification.RetryInterval == 0 {
				notification.RetryInterval = 30 * time.Second
			}
		}
	}
	return cfg, nil
}

// IsEnabled returns true when the flag is unset, so configs can stay concise.
func (w WebhookConfig) IsEnabled() bool {
	return w.Enabled == nil || *w.Enabled
}

func (r RouteConfig) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// AllowsWebhook controls whether a route may be selected by a specific webhook.
// When webhook_names is empty, the route accepts all webhook entry points.
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

func (t TargetConfig) IsCallbackEnabled() bool {
	if t.CallbackEnabled != nil {
		return *t.CallbackEnabled
	}
	return strings.TrimSpace(t.CallbackURL) != ""
}

func (n NotificationConfig) IsEnabled() bool {
	return n.Enabled == nil || *n.Enabled
}

// LoadAgentConfig loads agent.yaml and fills runtime defaults.
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
	if cfg.DockerConfigDir == "" {
		cfg.DockerConfigDir = "/var/lib/harbor-relay-agent/docker-config"
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
