---
title: 全流程示例
sidebar_position: 6
slug: /05-full-example
---

# 全流程示例

下面用一套完整示例把整个流程串起来。

## 场景

- 源 Harbor：`registry.example.com:9443`
- relay 域名：`relay.example.com:9443`
- 源项目：`team-a`
- 目标项目：`team-a-dr`
- 目标站点：`dc1`

## 一、用户拿到的信息

运维给用户：

- Harbor 地址：`registry.example.com:9443`
- Harbor 账号：由运维分配
- 源项目：`team-a`
- 同步结果通知渠道：项目群机器人

## 二、用户执行 push

```bash
docker login registry.example.com:9443
docker tag my-app:v1.0.0 registry.example.com:9443/team-a/my-app:v1.0.0
docker push registry.example.com:9443/team-a/my-app:v1.0.0
```

## 三、Harbor 发送 webhook

Harbor 向 relay 发：

```text
POST https://relay.example.com:9443/api/v1/harbor/webhook/team-a
Authorization: Bearer <team-a-webhook-token>
```

核心 webhook 内容：

- repository: `team-a/my-app`
- tag: `v1.0.0`
- digest: `sha256:...`

## 四、relay 创建任务

relay 根据 `relay.yaml`：

```yaml
routes:
  - name: team-a
    channel: team-a
    repository_patterns:
      - "team-a/**"
    target_sites:
      - dc1

targets:
  - name: dc1
    site_name: dc1
    target_registry: registry-dr.example.com:9443
    target_project: team-a-dr
```

生成任务：

- channel: `team-a`
- site_name: `dc1`
- source_pull_ref:
  - `registry.example.com:9443/team-a/my-app@sha256:...`
- target_repository:
  - `team-a-dr/my-app`

## 五、agent 领任务

`dc1` 的 agent 配置：

```yaml
site_name: dc1
channels:
  - team-a
```

因此它会消费这条任务。

## 六、agent 执行同步

实际动作：

1. 登录源仓库
2. 拉取

```text
registry.example.com:9443/team-a/my-app@sha256:...
```

3. 登录目标仓库
4. 重新打 tag

```text
registry-dr.example.com:9443/team-a-dr/my-app:v1.0.0
```

5. push 到目标项目

## 七、relay 接收结果

agent 回报：

- status: `DONE`
- target_refs:
  - `registry-dr.example.com:9443/team-a-dr/my-app:v1.0.0`
- target_ref_descriptors:
  - `registry-dr.example.com:9443/team-a-dr/my-app:v1.0.0@sha256:...`

## 八、通知与回调

relay 可以继续做两件事：

### 1. 发 callback

```text
POST https://ops.example.com/api/image-sync/callback
```

### 2. 发项目群机器人通知

例如纯文本：

```text
Team A 镜像同步完成 仓库 team-a/my-app 标签 v1.0.0 摘要 sha256:1234abcd 目标 dc1
```

## 九、最终结果

最终同时满足三件事：

- 用户知道自己 push 成功了
- 运维知道同步任务完成了
- 目标环境能在目标项目中拿到镜像

## 十、最常见的失败点

- webhook URL 写成双斜杠，导致 301 再变成 GET
- route 没命中 repository
- `site_name` 不匹配，agent 领不到任务
- source/target Harbor 凭据权限不足
- callback 地址存在，但没有实际接收服务
- 机器人接口限流，没有配通知队列参数
