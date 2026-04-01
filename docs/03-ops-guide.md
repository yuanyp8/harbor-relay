---
title: 运维部署手册
sidebar_position: 4
slug: /03-ops-guide
---

# 运维部署手册

这份文档面向部署和维护 `harbor-relay` 的人员。

## 部署角色

通常会有两类节点：

- relay 节点
  - 接收 webhook
  - 提供 gRPC 与状态 API
  - 管理任务、callback 和通知
- agent 节点
  - 在目标环境执行 `docker pull / tag / push`

## 端口与域名建议

- Harbor：`registry.example.com:9443`
- Relay：`relay.example.com:9443`
- Docs：`docs.example.com:9443`

Relay 本机可以监听：

- HTTP：`127.0.0.1:18080`
- gRPC：`127.0.0.1:19090`

再通过外层 Caddy 统一暴露 `9443`。

## 一、准备安装包

Linux/macOS：

```bash
./build.sh --arch amd64
./build.sh --arch arm64
```

Windows PowerShell：

```powershell
.\build.ps1 -Arch amd64
.\build.ps1 -Arch arm64
```

产物：

- `dist/linux-amd64/harbor-relay-toolkit-linux-amd64.run`
- `dist/linux-arm64/harbor-relay-toolkit-linux-arm64.run`

## 二、安装 relay

```bash
chmod +x harbor-relay-toolkit-linux-amd64.run
sudo ./harbor-relay-toolkit-linux-amd64.run install --role relay
```

安装后会生成：

- `/usr/local/bin/harbor-relay`
- `/etc/harbor-relay/relay.yaml`
- `/etc/harbor-relay/examples/relay.yaml.example`
- `/etc/systemd/system/harbor-relay.service`

然后编辑配置：

```bash
sudo vi /etc/harbor-relay/relay.yaml
```

编辑完成后启用并设置开机自启：

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run activate --role relay
```

查看状态：

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run status --role relay
```

## 三、安装 agent

```bash
chmod +x harbor-relay-toolkit-linux-amd64.run
sudo ./harbor-relay-toolkit-linux-amd64.run install --role agent
```

安装后会生成：

- `/usr/local/bin/harbor-relay-agent`
- `/etc/harbor-relay/agent.yaml`
- `/etc/harbor-relay/examples/agent.yaml.example`
- `/etc/systemd/system/harbor-relay-agent.service`

编辑 agent 配置：

```bash
sudo vi /etc/harbor-relay/agent.yaml
```

启用并设置开机自启：

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run activate --role agent
```

查看状态：

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run status --role agent
```

## 四、relay 配置关键点

### 1. webhook

```yaml
webhooks:
  - name: team-a
    enabled: true
    path: /api/v1/harbor/webhook/team-a
    authorization: "Bearer replace-with-relay-webhook-token"
    source_registry: registry.example.com:9443
```

### 2. route

```yaml
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
```

### 3. target

```yaml
targets:
  - name: dc1
    site_name: dc1
    enabled: true
    target_registry: registry-dr.example.com:9443
    target_project: team-a-dr
    repository_prefix: ""
```

## 五、agent 配置关键点

```yaml
agent_id: dc1-agent-01
site_name: dc1
channels:
  - team-a

relay_address: 127.0.0.1:19090
relay_server_name: ""
insecure_skip_verify: false

source_registry: registry.example.com:9443
source_username: replace-with-source-robot-username
source_password: replace-with-source-robot-password

target_registry: registry-dr.example.com:9443
target_username: replace-with-target-robot-username
target_password: replace-with-target-robot-password
```

## 六、Harbor webhook 配置

在 Harbor 项目里新增 webhook：

- URL
  - `https://relay.example.com:9443/api/v1/harbor/webhook/team-a`
- Method
  - `POST`
- Header
  - `Authorization: Bearer <your-token>`

注意不要写成带双斜杠的路径，例如：

```text
https://relay.example.com:9443//api/v1/harbor/webhook/team-a
```

否则很多客户端会先 301 再改成 `GET`，最终导致 webhook 被 relay 拒绝。

## 七、Caddy 入口

Relay 与 Harbor 都可以复用 `9443`，前提是域名不同：

- `registry.example.com:9443` -> Harbor
- `relay.example.com:9443` -> relay

参考示例：

- [relay.example.com.9443.caddy](../deploy/caddy/relay.example.com.9443.caddy)

## 八、状态查询

```bash
curl http://127.0.0.1:18080/healthz
curl http://127.0.0.1:18080/api/v1/tasks
curl http://127.0.0.1:18080/api/v1/agents
curl http://127.0.0.1:18080/api/v1/notification-jobs
```

## 九、建议的上线顺序

1. 先跑通 Harbor `docker push`
2. 再跑通 relay webhook 入队
3. 再跑通 agent 领任务与推送
4. 最后再接 callback 和通知

这样排障路径最清晰。
