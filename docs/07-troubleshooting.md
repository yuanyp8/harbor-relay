---
title: 排障手册
sidebar_position: 8
slug: /07-troubleshooting
---

# 排障手册

## 1. Harbor push 成功，但没有看到任务

先查：

```bash
curl http://127.0.0.1:18080/api/v1/tasks
journalctl -u harbor-relay -n 100 --no-pager
```

重点看：

- webhook URL 是否正确
- Authorization 是否正确
- 是否误写成双斜杠路径

错误示例：

```text
https://relay.example.com:9443//api/v1/harbor/webhook/team-a
```

这类地址很容易触发 301，再被客户端改成 GET，最终 relay 返回 405。

## 2. 任务入队了，但没有 agent 消费

查：

```bash
curl http://127.0.0.1:18080/api/v1/agents
curl http://127.0.0.1:18080/api/v1/tasks
```

重点看：

- 任务的 `site_name`
- agent 的 `site_name`
- 任务的 `channel`
- agent 的 `channels`

最常见原因：

- agent 配的是 `dc1`
- 任务发给的是 `team-a`

二者不一致时，任务不会被消费。

## 3. agent 连不上 relay

如果看到：

```text
tls: first record does not look like a TLS handshake
```

说明 agent 正在用 TLS 去连一个明文 gRPC 地址。

本机调试时：

- relay 地址可以用 `127.0.0.1:19090`
- 此时 agent 应走明文 gRPC

正式通过 Caddy 暴露时：

- `relay_address: relay.example.com:9443`
- `relay_server_name: relay.example.com`

## 4. 同一台机器上 source/target Harbor 账号互相覆盖

症状：

- 手工 `docker push` 用一套 robot
- agent 执行又用另一套 robot
- 最后本机 `docker login` 凭据被覆盖

解决方式：

- agent 使用独立的 `docker_config_dir`
- source/target 凭据隔离
- 同一 Harbor 下不同项目时，使用顺序登录流程

## 5. Harbor 登录成功，但 token 地址不对

症状：

`docker login registry.example.com:9443` 失败，并跳转到了 `443`

通常是反向代理把 `Host` 改写成了不带端口的值。

排查方法：

```bash
curl -kI https://registry.example.com:9443/v2/
```

确认 `WWW-Authenticate` 里的 `realm` 是否带 `:9443`。

## 6. 通知没有发出去

查：

```bash
curl http://127.0.0.1:18080/api/v1/notification-jobs
journalctl -u harbor-relay -n 200 --no-pager
```

如果通知通道被限流，通常会看到类似 OneMsg 返回：

```json
{"msg":"群机器人发消息需要相隔1分钟","code":10002,"success":false}
```

这时任务不会丢，而是会进入本地通知队列，等待下一次重试。

## 7. callback 没成功

重点区分：

- webhook 是 Harbor -> relay
- callback 是 relay -> 外部系统

如果 `callback_url` 指向了一个不存在的服务，任务主流程依然可能已经成功；这时任务状态仍然是 `done`，但 `callback_status` 会记录为 `failed`，并且如果你为 `callback_failed` 配置了通知渠道，对应管家会收到回调失败告警。

解决方式：

- 要么先把 `callback_url` 留空
- 要么确保该 URL 后面确实有接收服务

## 8. 推荐的排障顺序

1. 先确认 Harbor `docker push` 成功
2. 再确认 webhook 成功进入 relay
3. 再确认任务是否创建
4. 再确认 agent 是否在线
5. 再确认 agent 是否领到任务
6. 最后才看 callback 与通知

这样可以避免把“核心同步链路”和“外围通知链路”混在一起排查。
