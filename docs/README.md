---
id: intro
title: 概览
slug: /intro
sidebar_position: 1
---

# Harbor Relay 文档

这套文档面向三类读者：

- 项目成员
  - 我该找谁申请 Harbor 账号
  - 我应该把镜像推到哪里
  - 推送后怎么知道同步有没有成功
- 运维人员
  - webhook 怎么接入
  - relay 和 agent 怎么部署
  - callback、群机器人、邮件通知应该怎么挂
- 开发与排障人员
  - repository、channel、site_name 到底怎么关联
  - 为什么任务入队了却没人消费
  - 日志、状态接口、通知队列该怎么看

## 推荐阅读顺序

1. [系统架构说明](/docs/01-system-overview)
2. [用户使用手册](/docs/02-user-guide)
3. [运维部署手册](/docs/03-ops-guide)
4. [通知与回调设计](/docs/04-notification-and-callback)
5. [全流程示例](/docs/05-full-example)
6. [接口与状态说明](/docs/06-api-reference)
7. [排障手册](/docs/07-troubleshooting)

## 一句话说明这套系统

用户只负责把镜像推到源 Harbor 项目；后面的任务拆分、远端拉取、目标仓库推送、结果回调和通知，都由运维侧部署好的 relay、agent 和通知服务来完成。

## 文档中的统一名词

- `source project`
  - 用户实际推送的 Harbor 项目
- `target project`
  - 远端或目标 Harbor 中最终落地的项目
- `webhook`
  - Harbor 推送事件进入 relay 的 HTTP 入口
- `channel`
  - relay 的调度频道
- `site_name`
  - 远端站点标识
- `agent`
  - 执行 `docker pull / tag / push` 的消费者
- `callback`
  - relay 在任务完成后主动 POST 给外部系统的结果通知

## 推荐域名规划

- `registry.example.com:9443`
  - Harbor
- `relay.example.com:9443`
  - relay 对外入口，承接 webhook 与 gRPC
- `docs.example.com:9443`
  - 文档站点
