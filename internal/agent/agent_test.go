package agent

import (
	"testing"

	relayv1 "github.com/yuanyp8/harbor-relay/gen/proto/relay/v1"
	"github.com/yuanyp8/harbor-relay/internal/config"
)

func TestShouldDelayTargetLogin_SameRegistryDifferentCredentials(t *testing.T) {
	task := &relayv1.TaskAssignment{
		SourceRegistry: "image.hm.metavarse.tech:9443",
		TargetRegistry: "image.hm.metavarse.tech:9443",
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
		SourceRegistry: "image.hm.metavarse.tech:9443",
		TargetRegistry: "image.hm.metavarse.tech:9443",
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
		SourceRegistry: "image.hm.metavarse.tech:9443",
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

func TestDockerArgs_UsesDedicatedConfigDir(t *testing.T) {
	a := &Agent{
		cfg: config.AgentConfig{
			DockerBinary:    "docker",
			DockerConfigDir: "/data/harbor-relay/docker-config",
		},
	}

	got := a.dockerArgs("pull", "image.hm.metavarse.tech:9443/test:v1")
	want := []string{"--config", "/data/harbor-relay/docker-config", "pull", "image.hm.metavarse.tech:9443/test:v1"}
	if len(got) != len(want) {
		t.Fatalf("unexpected arg length: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected args: got=%v want=%v", got, want)
		}
	}
}
