# Changelog

All notable changes to `harbor-relay` will be documented in this file.

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
