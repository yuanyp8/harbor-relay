package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRelayConfig_DefaultsAndWebhookNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relay.yaml")
	content := `
source_registry: image.hm.metavarse.tech:9443
webhooks:
  - name: default
    path: /api/v1/harbor/webhook
routes:
  - name: kube4-core
    repository_patterns:
      - "kube4/**"
    target_sites:
      - dc1
targets:
  - name: dc1
    target_registry: sealos.hub:5000
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	cfg, err := LoadRelayConfig(path)
	if err != nil {
		t.Fatalf("load relay config failed: %v", err)
	}

	if cfg.ServiceName != "harbor-relay" {
		t.Fatalf("unexpected service name: %s", cfg.ServiceName)
	}
	if cfg.HTTPListen != ":18080" {
		t.Fatalf("unexpected http listen: %s", cfg.HTTPListen)
	}
	if cfg.GRPCListen != ":19090" {
		t.Fatalf("unexpected grpc listen: %s", cfg.GRPCListen)
	}
	if len(cfg.Webhooks) != 1 || cfg.Webhooks[0].SourceRegistry != "image.hm.metavarse.tech:9443" {
		t.Fatalf("unexpected webhooks: %+v", cfg.Webhooks)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Channel != "kube4-core" {
		t.Fatalf("unexpected routes: %+v", cfg.Routes)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].SiteName != "dc1" {
		t.Fatalf("unexpected targets: %+v", cfg.Targets)
	}
}

func TestRouteConfig_AllowsWebhook(t *testing.T) {
	route := RouteConfig{
		Name:         "cmict",
		Channel:      "cmict-apps",
		WebhookNames: []string{"cmict-project"},
	}
	if !route.AllowsWebhook("cmict-project") {
		t.Fatal("expected route to allow cmict-project")
	}
	if route.AllowsWebhook("default") {
		t.Fatal("expected route to reject default webhook")
	}

	routeAny := RouteConfig{Name: "all", Channel: "default"}
	if !routeAny.AllowsWebhook("anything") {
		t.Fatal("expected route with empty webhook_names to allow all")
	}
}
