package agent

import (
	"path/filepath"
	"testing"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

func TestShouldDelayTargetLogin_SameRegistryDifferentCredentials(t *testing.T) {
	task := &relayv1.TaskAssignment{
		SourceRegistry: "registry.example.com:9443",
		TargetRegistry: "registry.example.com:9443",
	}
	cfg := config.AgentConfig{
		SourceUsername: "robot-source",
		SourcePassword: "source-pass",
		TargetUsername: "robot-target",
		TargetPassword: "target-pass",
	}

	if !shouldDelayTargetLogin(task, cfg) {
		t.Fatal("expected target login to be delayed for same registry with different credentials")
	}
}

func TestShouldDelayTargetLogin_SameRegistrySameCredentials(t *testing.T) {
	task := &relayv1.TaskAssignment{
		SourceRegistry: "registry.example.com:9443",
		TargetRegistry: "registry.example.com:9443",
	}
	cfg := config.AgentConfig{
		SourceUsername: "robot",
		SourcePassword: "shared-pass",
		TargetUsername: "robot",
		TargetPassword: "shared-pass",
	}

	if shouldDelayTargetLogin(task, cfg) {
		t.Fatal("expected target login not to be delayed when credentials are the same")
	}
}

func TestShouldDelayTargetLogin_DifferentRegistry(t *testing.T) {
	task := &relayv1.TaskAssignment{
		SourceRegistry: "registry.example.com:9443",
		TargetRegistry: "sealos.hub:5000",
	}
	cfg := config.AgentConfig{
		SourceUsername: "robot-source",
		SourcePassword: "source-pass",
		TargetUsername: "robot-target",
		TargetPassword: "target-pass",
	}

	if shouldDelayTargetLogin(task, cfg) {
		t.Fatal("expected target login not to be delayed for different registries")
	}
}

func TestRuntimeArgs_UsesDedicatedDockerConfigDir(t *testing.T) {
	a := &Agent{
		cfg: config.AgentConfig{
			DockerBinary:    "docker",
			DockerConfigDir: "/data/harbor-relay/docker-config",
		},
	}

	got := a.runtimeArgs("pull", "registry.example.com:9443/test:v1")
	want := []string{"--config", "/data/harbor-relay/docker-config", "pull", "registry.example.com:9443/test:v1"}
	if len(got) != len(want) {
		t.Fatalf("unexpected arg length: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected args: got=%v want=%v", got, want)
		}
	}
}

func TestRuntimeArgs_SealosDoesNotUseDockerConfigFlag(t *testing.T) {
	a := &Agent{
		cfg: config.AgentConfig{
			DockerBinary:    "sealos",
			DockerConfigDir: "/data/harbor-relay/docker-config",
		},
	}

	got := a.runtimeArgs("pull", "registry.example.com:9443/test:v1")
	want := []string{"pull", "registry.example.com:9443/test:v1"}
	if len(got) != len(want) {
		t.Fatalf("unexpected arg length: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected args: got=%v want=%v", got, want)
		}
	}
}

func TestLoginArgs_SealosUsesNativeLoginSyntax(t *testing.T) {
	a := &Agent{
		cfg: config.AgentConfig{
			DockerBinary:    "sealos",
			DockerConfigDir: "/data/harbor-relay/docker-config",
		},
	}

	got := a.loginArgs("registry.example.com:9443", "robot")
	want := []string{"login", "registry.example.com:9443", "-u", "robot", "--password-stdin"}
	if len(got) != len(want) {
		t.Fatalf("unexpected arg length: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected args: got=%v want=%v", got, want)
		}
	}
}

func TestCommandEnv_SealosUsesRegistryAuthFile(t *testing.T) {
	a := &Agent{
		cfg: config.AgentConfig{
			DockerBinary:    "sealos",
			DockerConfigDir: "/data/harbor-relay/docker-config",
		},
	}

	found := false
	want := "REGISTRY_AUTH_FILE=" + filepath.Join("/data/harbor-relay/docker-config", "auth.json")
	for _, entry := range a.commandEnv() {
		if entry == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected REGISTRY_AUTH_FILE to be injected for sealos runtime")
	}
}
