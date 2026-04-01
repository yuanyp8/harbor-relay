---
id: api-reference
title: 接口说明
sidebar_position: 7
slug: /06-api-reference
---

# 接口说明

本节主要说明 relay 对外暴露的 HTTP 接口。

## 健康检查

### `GET /healthz`

用途：

- 检查 relay HTTP 服务是否在线

示例：

```bash
curl http://127.0.0.1:18080/healthz
```

## Harbor webhook 接口

### `POST /api/v1/harbor/webhook/{name}`

用途：

- 接收 Harbor 推送事件

特点：

- method 必须为 `POST`
- path 必须与 `webhooks[].path` 匹配
- 如配置了 `authorization`，则请求头必须携带一致的 Bearer Token

示例：

```bash
curl -i -X POST 'http://127.0.0.1:18080/api/v1/harbor/webhook/team-a' \
  -H 'Authorization: Bearer replace-with-team-a-secret' \
  -H 'Content-Type: application/json' \
  -d '{
    "type":"PUSH_ARTIFACT",
    "operator":"alice",
    "event_data":{
      "repository":{"repo_full_name":"team-a/my-app"},
      "resources":[
        {"digest":"sha256:abc","tag":"v1.0.0"}
      ]
    }
  }'
```

## 任务列表

### `GET /api/v1/tasks`

用途：

- 查看当前任务列表
- 用于排查任务是否已经入队、是否完成

示例：

```bash
curl http://127.0.0.1:18080/api/v1/tasks
```

常见字段：

- `id`
- `event_id`
- `channel`
- `site_name`
- `repository`
- `digest`
- `tags`
- `source_pull_ref`
- `source_refs`
- `target_registry`
- `target_repository`
- `target_refs`
- `target_ref_descriptors`
- `status`
- `callback_status`
- `message`

## agent 列表

### `GET /api/v1/agents`

用途：

- 查看有哪些 agent 在线
- 查看 agent 属于哪个 `site_name`
- 查看 agent 订阅了哪些 `channels`

示例：

```bash
curl http://127.0.0.1:18080/api/v1/agents
```

## 通知队列

### `GET /api/v1/notification-jobs`

用途：

- 查看通知队列中还有哪些待发送或重试中的通知
- 排查 OneMsg 频控或通知失败

示例：

```bash
curl http://127.0.0.1:18080/api/v1/notification-jobs
```

可重点关注：

- `channel_name`
- `event`
- `status`
- `attempts`
- `next_attempt_at`
- `last_error`

## gRPC 接口

agent 与 relay 之间使用 gRPC 长连接通信。

运维通常不需要手工调用 gRPC 接口，但需要知道：

- gRPC 监听地址由 `grpc_listen` 决定
- 通过 Caddy 对外暴露时，应按 `Content-Type: application/grpc*` 分流到 gRPC 后端
- gRPC 和 HTTP 可以共享同一个外部端口，只要域名和分流规则正确

## callback 出站请求

callback 不是 relay 对外暴露的“入站接口”，而是 relay 主动调用外部系统的“出站接口”。

当 `callback_enabled: true` 时，relay 会在任务状态变化后向 `callback_url` 发起 `POST`，请求体中会携带：

- 任务 ID
- 事件 ID
- 站点
- 频道
- 源镜像
- 目标镜像
- 状态
- 附加说明

如果 callback 失败：

- 任务主状态仍可能是 `done`
- 但 `callback_status` 会标记为失败
- 可单独触发 `callback_failed` 通知
