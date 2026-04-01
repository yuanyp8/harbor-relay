---
id: user-guide
title: 用户使用手册
sidebar_position: 3
slug: /02-user-guide
---

# 用户使用手册

这份文档给项目成员、交付成员或应用开发人员使用。

用户侧不需要理解 relay 和 agent 的内部实现，只需要知道如何正确地把镜像推送到源 Harbor，并通过通知或状态页确认远端同步是否完成。

## 你需要准备什么

用户侧只需要三样东西：

1. 一个由运维提供的 Harbor 账号或 robot 账号
2. 一个明确的源项目路径
3. 你已经构建好的本地镜像

用户侧不需要准备：

- 目标仓库账号
- gRPC 地址
- callback 地址
- agent 部署信息

## 你要向运维确认哪些信息

建议一次性确认以下内容：

- Harbor 地址
  - 例如 `registry.example.com:9443`
- 你的源项目
  - 例如 `team-a`
- 你的 Harbor 用户名和密码
- 镜像会被同步到哪些环境
- 同步结果通过什么渠道通知
  - 群机器人
  - 邮件
  - 状态页

## 日常操作流程

### 1. 登录 Harbor

```bash
docker login registry.example.com:9443
```

### 2. 给本地镜像打 tag

假设本地镜像是：

```text
my-app:v1.0.0
```

运维告诉你的源项目是：

```text
team-a
```

则应这样打 tag：

```bash
docker tag my-app:v1.0.0 registry.example.com:9443/team-a/my-app:v1.0.0
```

### 3. 推送镜像

```bash
docker push registry.example.com:9443/team-a/my-app:v1.0.0
```

### 4. 观察同步结果

正常情况下，系统会自动完成：

1. Harbor 接收镜像
2. Harbor webhook 通知 relay
3. relay 创建同步任务
4. 远端 agent 拉取源镜像并推送到目标项目
5. 运维侧收到同步成功或失败通知

## `docker push` 成功不等于远端同步成功

`docker push` 成功，只表示你已经把镜像推到了源 Harbor。

远端同步是否成功，要看：

- relay 是否成功入队
- 是否有 agent 消费任务
- 目标仓库是否推送成功
- 通知或状态页是否显示完成

## 用户如何知道同步状态

推荐运维至少提供其中一种方式：

- 群机器人消息
- 邮件通知
- 状态页

如果运维启用了 `queued` 和 `done` 这两个通知，用户一般会看到：

- 已进入同步队列
- 已完成远端同步

## 推荐镜像命名方式

建议统一使用：

```text
<project>/<repository>:<version>
```

例如：

- `team-a/my-app:v1.0.0`
- `platform/mysql:8.0.45`
- `platform/redis-exporter:v1.54.0`

## 一个完整示例

### 你从运维处拿到的信息

- Harbor 地址：`registry.example.com:9443`
- Harbor 用户名和密码：由运维提供
- 源项目：`team-a`

### 你本地执行

```bash
docker login registry.example.com:9443
docker tag my-app:v1.0.0 registry.example.com:9443/team-a/my-app:v1.0.0
docker push registry.example.com:9443/team-a/my-app:v1.0.0
```

### 系统后端做了什么

- relay 收到 `team-a/my-app:v1.0.0`
- route 识别该仓库属于 `team-a` 频道
- 远端 agent 拉取：
  - `registry.example.com:9443/team-a/my-app@sha256:...`
- 远端 agent 推送：
  - `registry-dr.example.com:9443/team-a-dr/my-app:v1.0.0`

### 你最终应该看到什么

- 源 Harbor 上 push 成功
- 群机器人或邮件收到“同步完成”通知
- 或运维确认目标项目中已经出现该镜像

## 用户侧的边界

你不需要做这些事：

- 不需要手工触发 webhook
- 不需要配置 callback
- 不需要知道 relay 的 gRPC 地址
- 不需要登录目标仓库

如果遇到同步异常，请把下面这些信息发给运维：

- 完整镜像名
- 推送时间
- Harbor 项目名
- 看到的错误信息
