---
title: 通知与回调设计
sidebar_position: 5
slug: /04-notification-and-callback
---

# 通知与回调设计

`harbor-relay` 同时支持两类“任务结果传播”能力：

- `callback_url`
  - 面向外部系统，发送结构化 JSON
- `notifications`
  - 面向通知渠道，发送群机器人或其他网关消息

## callback_url 是什么

`callback_url` 是 relay 在任务完成后主动发起的 HTTP POST。

它适合接到：

- 运维平台
- 状态中心
- 审批平台
- 告警聚合服务

不建议直接把 callback 当用户通知手段，因为 callback 更偏系统对系统。

## callback_url 可以和 relay 用同一个域名吗

可以，但建议：

- 同一个域名
- 不同路径
- 由 Caddy 分流到不同后端

例如：

- `https://relay.example.com:9443/api/v1/harbor/webhook/team-a`
  - Harbor -> relay webhook
- `https://relay.example.com:9443/api/v1/tasks`
  - 用户或运维查询状态
- `https://relay.example.com:9443/api/image-sync/callback`
  - relay -> callback-consumer

也就是说，可以共用 `relay.example.com`，但 callback 最好由专门的接收服务处理，而不是让 relay 自己回调自己。

## notifications 是什么

`notifications` 是 relay 内置的通知通道能力。

当前重点支持：

- OneMsg 机器人

后续可扩展：

- 邮件
- 企业微信
- 钉钉
- 自定义 HTTP 网关

## 为什么通知队列要单独做

通知接口经常有这些限制：

- 每分钟只能发一条
- 同一个机器人不能并发发多条
- 短时间内多次调用会返回限流错误

因此 relay 里做了独立通知队列，而不是在任务主链路里同步直发。

## 当前限流策略

每个通知机器人都是一个独立 channel。

它拥有自己的：

- `min_interval`
- `retry_interval`
- `max_attempts`

如果 OneMsg 返回：

```json
{"msg":"群机器人发消息需要相隔1分钟","code":10002,"success":false}
```

relay 会把这条通知重新放回本地持久化队列，并在稍后再次投递。

这意味着：

- 一个机器人被限流，不会阻塞别的机器人
- relay 重启后，未投递完成的通知仍可恢复

## 推荐的事件分发方式

通常建议不同角色看不同事件：

- 运维群
  - `queued`
  - `failed`
  - `callback_failed`
- 项目群
  - `done`

这样用户不会被低价值消息淹没，运维也不会错过需要介入的异常。

## relay.yaml 示例

```yaml
targets:
  - name: dc1
    site_name: dc1
    target_registry: registry-dr.example.com:9443
    target_project: team-a-dr
    callback_url: "https://ops.example.com/api/image-sync/callback"
    callback_token: "replace-with-callback-token"
    notifications:
      - name: team-a-project-group
        type: onemsg_robot
        endpoint: "https://office.example.com/onemsg-api/robot/pushToRobot"
        robot_key: "replace-with-project-group-robot-key"
        title_prefix: "Team A"
        events:
          - done
        min_interval: 1m
        retry_interval: 1m
        max_attempts: 20
      - name: platform-ops-group
        type: onemsg_robot
        endpoint: "https://office.example.com/onemsg-api/robot/pushToRobot"
        robot_key: "replace-with-ops-group-robot-key"
        title_prefix: "Platform Ops"
        events:
          - queued
          - failed
          - callback_failed
        min_interval: 1m
        retry_interval: 1m
        max_attempts: 20
```

## 通知消息应该怎么设计

建议使用纯文本，不依赖 Markdown，不依赖换行。

推荐单条文本包含：

- 环境或业务名
- 仓库名
- tag
- digest 缩略值
- 目标站点
- 最终状态

例如：

```text
Team A 镜像同步完成 仓库 team-a/my-app 标签 v1.0.0 摘要 sha256:1234abcd 目标 dc1
```

## 用户如何看到同步进度

推荐组合是：

1. relay 状态 API
2. 项目群 done 通知
3. 运维群 queued / failed 通知
4. callback 给状态中心

这四层组合起来，基本能覆盖：

- 用户体验
- 运维介入
- 系统审计
- 自动化集成
