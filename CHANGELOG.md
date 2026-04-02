# Changelog

All notable changes to `harbor-relay` will be documented in this file.

## v0.0.3

Release date: 2026-04-02

Highlights:

- Added agent runtime compatibility for `sealos` so the same sync flow can run on environments that do not use the Docker CLI.
- Kept the existing Docker path unchanged, including dedicated `docker_config_dir` handling through `--config`.
- Switched `sealos` login flow to `REGISTRY_AUTH_FILE` and native `sealos login` arguments to avoid `unknown flag: --config`.
- Added unit tests covering Docker and Sealos runtime argument handling.
- Updated agent config examples and troubleshooting docs for Sealos-based target environments.

Recommended upgrade steps:

- Replace the deployed `harbor-relay-agent` binary with the new `v0.0.3` build.
- If the remote runtime is Sealos, set `docker_binary: sealos` in `agent.yaml`.
- Keep `docker_config_dir` configured; the agent now maps it to `REGISTRY_AUTH_FILE` automatically when running Sealos.

## v0.0.2

Release date: 2026-04-02

Highlights:

- Fixed the `harbor-relay.service` restart hang caused by long-lived gRPC agent streams not exiting before `systemd` stop timeout.
- Added bounded gRPC shutdown logic with graceful-stop timeout and force-stop fallback.
- Added unit tests covering graceful shutdown and forced shutdown fallback.
- Updated the bundled `systemd` unit with `TimeoutStopSec=20` to make restart behavior predictable.
- Updated troubleshooting guidance for relay shutdown and restart behavior.

Recommended upgrade steps:

- Replace the deployed relay binary with the new `v0.0.2` build.
- Update `harbor-relay.service` so it contains `TimeoutStopSec=20`.
- Run `systemctl daemon-reload` before restarting the service.
