---
id: intro
title: 概览
slug: /intro
sidebar_position: 1
---

# Harbor Relay 文档

这套文档面向三类读者：

- 项目成员
  - 我要向谁申请 Harbor 账号
  - 我要把镜像推到哪里
  - 推送后怎么知道同步结果
- 运维人员
  - `.run` 安装包怎么部署 relay 和 agent
  - webhook、Caddy、callback、通知怎么接
  - 任务队列、通知队列、状态接口怎么排障
- 开发和排障人员
  - `repository`、`channel`、`site_name` 怎么关联
  - 为什么 webhook 收到了但任务没有被消费
  - 为什么同步完成但 callback 失败

## 推荐阅读顺序

1. [系统架构说明](/docs/01-system-overview)
2. [用户使用手册](/docs/02-user-guide)
3. [运维部署手册](/docs/03-ops-guide)
4. [通知与回调设计](/docs/04-notification-and-callback)
5. [全流程示例](/docs/05-full-example)
6. [接口说明](/docs/06-api-reference)
7. [排障手册](/docs/07-troubleshooting)

## 一句话说明这套系统

用户只负责把镜像推到源 Harbor 项目；后面的任务拆分、远端拉取、目标仓库推送、结果回调和通知，都由运维侧部署好的 relay、agent 和通知系统完成。

## 统一术语

- `source project`
  - 用户实际推送的 Harbor 项目
- `target project`
  - 远端或目标仓库中最终落地的项目
- `webhook`
  - Harbor 发给 relay 的 HTTP 入站事件
- `channel`
  - relay 内部调度频道
- `site_name`
  - 远端站点标识
- `agent`
  - 执行 `docker pull / tag / push` 的消费者
- `callback`
  - relay 在任务完成后主动 POST 给外部系统的结果通知
- `notification`
  - relay 直接发送给群机器人、告警网关、邮件中间件的文本通知

## 推荐域名规划

- `registry.example.com:9443`
  - Harbor 对外入口
- `relay.example.com:9443`
  - relay 对外入口，承接 webhook、状态 API 和 gRPC
- `docs.example.com:9443`
  - 文档站

## 当前版本的交付方式

项目已经支持使用 `.run` 安装包进行标准化交付：

- `install --role relay`
- `install --role agent`
- `activate --role relay`
- `activate --role agent`
- `status --role all`

这意味着你可以把 relay 和 agent 当成两个独立的 `systemd` 服务来部署和运维，而不需要手工摆放二进制和 unit 文件。
