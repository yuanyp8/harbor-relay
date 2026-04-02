---
id: troubleshooting
title: 排障手册
sidebar_position: 8
slug: /07-troubleshooting
---

# 排障手册

本手册列出目前最常见的现场问题和排查方法。

## 1. Harbor push 成功，但没有任何同步任务

优先检查：

- Harbor webhook URL 是否写对
- 是否误写成双斜杠 `//`
- relay 的 `authorization` 是否与 Harbor 配置一致
- `repository_patterns` 是否能命中仓库名

检查命令：

```bash
curl http://127.0.0.1:18080/api/v1/tasks
journalctl -u harbor-relay -n 200 --no-pager
```

## 2. webhook 收到了，但任务没有被 agent 消费

最常见原因：

- `site_name` 不匹配
- `channels` 不匹配

例如：

- 任务发给 `site_name=yunnan-mid`
- 但 agent 配的是 `site_name=dc1`

检查命令：

```bash
curl http://127.0.0.1:18080/api/v1/agents
curl http://127.0.0.1:18080/api/v1/tasks
```

## 3. agent 报 `unexpected content-type "application/json"`

原因：

- gRPC 请求被转发到了 relay 的 HTTP 端口

通常是 Caddy 分流规则写错了。

正确思路：

- HTTP -> `127.0.0.1:18080`
- gRPC -> `h2c://127.0.0.1:19090`

## 4. agent 报 `502 Bad Gateway`

原因通常是：

- Caddy 已经识别出 gRPC
- 但后端 `127.0.0.1:19090` 没有监听或不可达

检查命令：

```bash
systemctl status harbor-relay --no-pager
ss -lntp | egrep '18080|19090'
nc -vz 127.0.0.1 19090
journalctl -u caddy -n 100 --no-pager
```

## 5. `activate` 提示配置里还有 placeholders

旧版安装器会扫描整个配置文件，只要看到 `replace-with-*` 就阻止激活。

新版本行为：

- 如果 `callback_enabled: false`，不会再要求 `callback_token`
- 如果某个通知通道 `enabled: false`，不会再要求它的 endpoint 或 robot key

如果仍被拦住：

- 确认使用的是新安装包
- 或直接把对应占位值清空为 `""`

## 6. 源仓库和目标仓库是同一个 Harbor，robot 账号冲突

这是常见场景。

解决方案：

- agent 先用源账号登录并拉取
- 再切换到目标账号登录并推送
- 同时使用独立的 `docker_config_dir`，避免污染 `/root/.docker/config.json`

### `sealos login` 报 `unknown flag: --config`

现象：

```text
source login failed: exit status 1: Error: unknown flag: --config
```

原因：

- agent 机器实际使用的是 `sealos`，不是 `docker`
- `sealos login` 不接受 Docker 风格的 `--config`

处理方式：

- 在 `agent.yaml` 里把 `docker_binary` 改成 `sealos`
- 保留 `docker_config_dir`
- 新版本 agent 会自动改用 `REGISTRY_AUTH_FILE` 和 `sealos login`

## 7. 同步完成了，但 callback 失败

这不一定表示镜像同步失败。

当前系统语义：

- `done`
  - 表示镜像已经同步完成
- `callback_failed`
  - 表示 callback 投递失败

如果只看到 `callback_failed`，应优先检查 callback 接收方，不要误判成镜像未同步。

## 8. OneMsg 通知没有发出或被限流

典型返回：

```json
{"msg":"群机器人发消息需要相隔1分钟","code":10002,"success":false}
```

系统行为：

- 进入通知队列
- 到达下一次可发送时间后自动重试

检查命令：

```bash
curl http://127.0.0.1:18080/api/v1/notification-jobs
```

## 9. 消息都发出去了，但用户说“看不懂状态”

建议：

- 面向用户，只保留 `queued / done / failed`
- `pulling / pushing` 更适合给运维群或值班群
- `callback_failed` 应归类为运维侧消息，而不是用户侧消息

## 10. 如何确认目标仓库里真的有镜像

最直接的方法是到目标站点执行：

```bash
docker pull <target-registry>/<project>/<repo>:<tag>
```

或者由目标仓库管理员确认镜像已入库。

## 11. 基础排障命令

### relay

```bash
systemctl status harbor-relay --no-pager
journalctl -u harbor-relay -n 200 --no-pager
curl http://127.0.0.1:18080/healthz
curl http://127.0.0.1:18080/api/v1/tasks
curl http://127.0.0.1:18080/api/v1/agents
curl http://127.0.0.1:18080/api/v1/notification-jobs
```

### agent

```bash
systemctl status harbor-relay-agent --no-pager
journalctl -u harbor-relay-agent -n 200 --no-pager
```

### Caddy

```bash
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
systemctl reload caddy
journalctl -u caddy -n 200 --no-pager
```

## 12. `systemctl restart harbor-relay.service` 长时间卡住

现象：

- 前台执行 `systemctl restart harbor-relay.service` 很久才返回
- 日志里已经出现 `shutdown requested`
- 旧进程还在持续打印 agent `heartbeat`
- 最后被 `systemd` 以 `State 'stop-sigterm' timed out. Killing.` 强制结束

原因：

- relay 收到 `SIGTERM` 后，HTTP 服务可以较快退出
- 但 gRPC agent 长连接仍在优雅停机阶段阻塞
- `grpc.GracefulStop()` 会等待活动中的双向流自己结束，如果 agent 持续发心跳，就会拖长停机时间

解决方法：

- 升级到 `v0.0.2` 或更新版本
- 替换新的 `harbor-relay` 二进制
- 确保 `harbor-relay.service` 中包含：

```ini
TimeoutStopSec=20
```

- 更新后执行：

```bash
systemctl daemon-reload
systemctl restart harbor-relay.service
journalctl -u harbor-relay.service -n 50 --no-pager
```

新版本行为：

- 先尝试 gRPC 优雅停机
- 如果超过内部超时仍未退出，会自动切换到强制停止
- 避免 `systemd` 再卡到默认的 stop timeout
