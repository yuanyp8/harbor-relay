---
title: 接口与状态说明
sidebar_position: 7
slug: /06-api-reference
---

# 接口与状态说明

## 健康检查

### `GET /healthz`

用途：

- 检查 relay HTTP 服务是否存活

示例：

```bash
curl http://127.0.0.1:18080/healthz
```

## Harbor webhook 入口

### `POST /api/v1/harbor/webhook/<name>`

用途：

- 接收 Harbor 项目 webhook

请求头：

- `Authorization: Bearer <token>`

成功返回：

```json
{
  "event_id": "abc123",
  "repository": "team-a/my-app",
  "status": "queued",
  "target_sites": ["dc1"],
  "task_count": 1
}
```

## 任务列表

### `GET /api/v1/tasks`

用途：

- 查看所有任务
- 排查任务是否入队
- 排查状态是否推进

重要字段：

- `channel`
- `site_name`
- `repository`
- `digest`
- `tags`
- `source_pull_ref`
- `source_refs`
- `target_repository`
- `target_refs`
- `target_ref_descriptors`
- `status`

## agent 列表

### `GET /api/v1/agents`

用途：

- 查看当前已连接 agent
- 查看 `site_name`、`channels`、心跳时间

## 通知任务列表

### `GET /api/v1/notification-jobs`

用途：

- 查看通知是否入队
- 查看是否被限流
- 查看下次重试时间

重要字段：

- `channel_name`
- `event`
- `status`
- `attempts`
- `next_run_at`

## 任务状态枚举

常见状态：

- `PENDING`
  - 已入队，等待 agent 领取
- `ASSIGNED`
  - 已分配给 agent
- `PULLING`
  - 正在从源仓库拉取
- `PUSHING`
  - 正在向目标仓库推送
- `DONE`
  - 已完成
- `FAILED`
  - 失败
- `CALLBACK_PENDING`
  - 主任务已完成，但回调失败或待重试

## 推荐的排障顺序

1. `/healthz`
2. `/api/v1/agents`
3. `/api/v1/tasks`
4. `/api/v1/notification-jobs`
5. `journalctl -u harbor-relay`
6. `journalctl -u harbor-relay-agent`
