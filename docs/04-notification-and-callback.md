---
id: notification-and-callback
title: 通知与回调设计
sidebar_position: 5
slug: /04-notification-and-callback
---

# 通知与回调设计

`harbor-relay` 同时支持两类出站能力：

- `callback`
  - 适合运维平台、状态页、工单系统、统一消息中台
- `notification`
  - 适合群机器人、短信网关、邮件网关等直接通知

两者可以同时开启，也可以分别关闭。

## callback 和 notification 的区别

### callback

callback 是 relay 在任务状态变化后，主动向外部系统发起的结构化 HTTP POST。

适合：

- 运维平台落库
- 状态页展示
- 审计和留痕
- 再由外部平台决定是否转发消息

### notification

notification 是 relay 内置的文本通知通道。

适合：

- 直接发群机器人
- 直接对接告警中间件
- 让用户第一时间看到“已入队 / 已完成 / 已失败”

## 可以独立开关

### callback

在 `targets[]` 中使用：

```yaml
callback_enabled: false
callback_url: "https://ops.example.com/api/image-sync/callback"
callback_token: "replace-with-callback-token"
```

说明：

- `callback_enabled: false`
  - callback 完全关闭
- `callback_enabled: true`
  - callback 启用，需要 `callback_url`

### notifications

在 `targets[]` 中使用：

```yaml
notifications:
  - name: steward-done
    type: onemsg_robot
    enabled: true
    endpoint: "https://office.example.com/onemsg-api/robot/pushToRobot"
    robot_key: "xxx"
    events:
      - done
```

如果某个通知通道不需要启用：

```yaml
enabled: false
```

## 支持的事件

当前推荐使用：

- `queued`
  - webhook 已接收，任务已入队
- `pulling`
  - agent 正在拉取源镜像
- `pushing`
  - agent 正在推送目标镜像
- `done`
  - 镜像同步主流程已完成
- `failed`
  - 镜像同步主流程失败
- `callback_failed`
  - 镜像同步主流程已经完成，但 callback 投递失败

## 为什么 `done` 和 `callback_failed` 分开

这是一个非常重要的设计点。

如果镜像已经成功同步到目标仓库，但 callback 超时了，那么：

- 主流程应该记为 `done`
- callback 单独记为 `failed`

否则用户会误以为“镜像没同步完”，这会导致判断混乱。

因此当前语义是：

- `done`
  - 表示镜像同步已经成功
- `callback_failed`
  - 表示同步完成后的通知回调失败

## OneMsg 频控设计

OneMsg 机器人通常有限制，例如：

```json
{"msg":"群机器人发消息需要相隔1分钟","code":10002,"success":false}
```

为了解决这个问题，relay 内置了通知队列和重试逻辑。

### 设计原则

- 每个机器人单独限流
- 每个机器人单独队列
- 一个机器人被限流，不影响其他机器人
- 被限流的通知进入本地持久化队列
- 到达下一次可发送时间后自动重试

### 相关配置

```yaml
notifications:
  - name: steward-done
    type: onemsg_robot
    enabled: true
    endpoint: "https://office.example.com/onemsg-api/robot/pushToRobot"
    robot_key: "xxx"
    events:
      - done
    timeout: 10s
    min_interval: 65s
    retry_interval: 30s
    max_attempts: 0
```

说明：

- `min_interval`
  - 同一机器人最小发送间隔
- `retry_interval`
  - 失败后下一次尝试间隔
- `max_attempts: 0`
  - 无限重试

## 推荐通知分工

比较清晰的做法是“一个事件对应一个管家机器人”：

- 管家 A：`queued`
- 管家 B：`pulling`
- 管家 C：`pushing`
- 管家 D：`done`
- 管家 E：`failed`
- 管家 F：`callback_failed`

这样可以避免：

- 同一个机器人一分钟内收到多个不同阶段消息
- 业务消息和故障消息混在一起

## 消息格式建议

通知内容建议保持：

- 纯文本
- 多行
- 关键信息靠前
- 不依赖 Markdown

推荐字段顺序：

1. 标题
2. 站点
3. 频道
4. 源镜像
5. 目标镜像
6. 摘要
7. 标签
8. 说明
9. 任务 ID

## callback 地址可以和 relay 同域名吗

可以，但建议：

- 同一个域名
- 不同路径
- 后端必须是独立的 callback consumer

例如：

- `https://relay.example.com:9443/api/v1/harbor/webhook/...`
  - relay 自己接 webhook
- `https://relay.example.com:9443/api/image-sync/callback`
  - 由另一个服务接 callback

不要让 relay 自己 callback 给自己没有实现的路径。

## 推荐演进路径

### 初始阶段

- 先只开 `notification`
- `callback_enabled: false`

### 稳定阶段

- 接入统一运维平台
- 打开 `callback_enabled`
- callback 成功后再由运维平台转群、转邮件、转状态页

### 成熟阶段

- notification 只保留面向用户的关键阶段
- callback 负责结构化事件沉淀和审计
