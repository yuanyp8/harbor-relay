---
id: full-example
title: 全流程示例
sidebar_position: 6
slug: /05-full-example
---

# 全流程示例

本节给出一个从用户 `docker push` 到远端同步完成的完整例子。

## 场景

- 源 Harbor：
  - `registry.example.com:9443`
- relay：
  - `relay.example.com:9443`
- 目标仓库：
  - `registry-dr.example.com:9443`
- 源项目：
  - `team-a`
- 目标项目：
  - `team-a-dr`
- 站点：
  - `dc1`
- 频道：
  - `team-a`

## relay 配置片段

```yaml
webhooks:
  - name: team-a
    enabled: true
    path: /api/v1/harbor/webhook/team-a
    authorization: "Bearer replace-with-team-a-secret"
    source_registry: registry.example.com:9443

routes:
  - name: team-a
    channel: team-a
    enabled: true
    webhook_names:
      - team-a
    repository_patterns:
      - "team-a/**"
    target_sites:
      - dc1

targets:
  - name: dc1
    site_name: dc1
    enabled: true
    target_registry: registry-dr.example.com:9443
    target_project: team-a-dr
    callback_enabled: false
```

## agent 配置片段

```yaml
site_name: dc1
channels:
  - team-a

relay_address: relay.example.com:9443
relay_server_name: relay.example.com

source_registry: registry.example.com:9443
source_username: robot$team-a+source
source_password: replace-with-source-password

target_registry: registry-dr.example.com:9443
target_username: robot$team-a-dr+target
target_password: replace-with-target-password
```

## 用户实际操作

### 1. 登录源 Harbor

```bash
docker login registry.example.com:9443
```

### 2. 给镜像打 tag

```bash
docker tag my-app:v1.0.0 registry.example.com:9443/team-a/my-app:v1.0.0
```

### 3. 推送

```bash
docker push registry.example.com:9443/team-a/my-app:v1.0.0
```

## Harbor 发送的 webhook

Harbor 发给 relay 的核心信息大致包括：

- `repository.repo_full_name`
  - `team-a/my-app`
- `resources[].tag`
  - `v1.0.0`
- `resources[].digest`
  - `sha256:...`

## relay 做了什么

### 1. 识别 webhook path

命中：

```text
/api/v1/harbor/webhook/team-a
```

因此使用：

- `webhook_name = team-a`
- `source_registry = registry.example.com:9443`

### 2. 路由仓库

仓库名：

```text
team-a/my-app
```

命中规则：

```text
team-a/**
```

因此进入：

```text
channel = team-a
```

### 3. 展开到站点

该 route 指定：

```text
target_sites = [dc1]
```

所以 relay 创建一条发往 `dc1` 的任务。

## 任务里的关键字段

创建的任务大致包含：

- `repository`
  - `team-a/my-app`
- `digest`
  - `sha256:...`
- `tags`
  - `["v1.0.0"]`
- `source_pull_ref`
  - `registry.example.com:9443/team-a/my-app@sha256:...`
- `source_refs`
  - `registry.example.com:9443/team-a/my-app:v1.0.0@sha256:...`
- `target_repository`
  - `team-a-dr/my-app`

## agent 做了什么

### 1. 连接 relay

agent 订阅：

- `site_name = dc1`
- `channels = [team-a]`

因此可以消费这条任务。

### 2. 拉取源镜像

```bash
docker pull registry.example.com:9443/team-a/my-app@sha256:...
```

### 3. 打目标 tag

```bash
docker tag \
  registry.example.com:9443/team-a/my-app@sha256:... \
  registry-dr.example.com:9443/team-a-dr/my-app:v1.0.0
```

### 4. 推送目标镜像

```bash
docker push registry-dr.example.com:9443/team-a-dr/my-app:v1.0.0
```

## 用户和运维分别会看到什么

### 用户侧

- Harbor push 成功
- 群通知：
  - 已入队
  - 拉取中
  - 推送中
  - 已完成

### 运维侧

- `/api/v1/tasks` 中任务状态从 `pending` 变成 `done`
- `/api/v1/agents` 中可看到 `dc1` agent 在线
- 如配置了 callback，外部平台还能收到结构化状态

## 最终结果

目标仓库中会出现：

```text
registry-dr.example.com:9443/team-a-dr/my-app:v1.0.0
```

而日志与状态接口中还会保留更适合审计和排障的描述符：

```text
registry-dr.example.com:9443/team-a-dr/my-app:v1.0.0@sha256:...
```
