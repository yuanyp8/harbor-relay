---
id: ops-guide
title: 运维部署手册
sidebar_position: 4
slug: /03-ops-guide
---

# 运维部署手册

本手册面向部署和维护 `harbor-relay` 的运维人员。

目标：

- 使用 `.run` 安装包标准化安装 relay 和 agent
- 通过 Caddy 暴露 Harbor、relay 和文档站
- 正确配置 webhook、route、target、notification、callback

## 部署建议

推荐角色拆分如下：

- Harbor 所在机器
  - 安装 `relay`
  - 可选同时安装 docs 站
- 每个远端站点
  - 安装一个或多个 `agent`

通常不建议在所有机器上同时运行 relay 和 agent。

## 前置条件

### relay 机器

- Linux x86_64 或 arm64
- `systemd`
- 网络可被 Harbor webhook 访问
- 如果对外通过 Caddy 暴露，还需要：
  - Caddy
  - 已配置好的 TLS 入口

### agent 机器

- Linux x86_64 或 arm64
- `systemd`
- Docker 已安装
- 能访问源仓库
- 能访问目标仓库

## 安装 relay

### 1. 安装程序

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run install --role relay
```

安装器会放置：

- `/usr/local/bin/harbor-relay`
- `/etc/harbor-relay/relay.yaml`
- `/etc/harbor-relay/examples/relay.yaml.example`
- `/etc/systemd/system/harbor-relay.service`

### 2. 编辑配置

```bash
sudo vi /etc/harbor-relay/relay.yaml
```

至少需要填这几类项：

- `webhooks`
  - `path`
  - `authorization`
- `routes`
  - `repository_patterns`
  - `channel`
  - `target_sites`
- `targets`
  - `target_registry`
  - `target_project` 或 `repository_prefix`

### 3. 激活服务

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run activate --role relay
```

### 4. 查看状态

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run status --role relay
systemctl status harbor-relay --no-pager
```

## 安装 agent

### 1. 安装程序

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run install --role agent
```

安装器会放置：

- `/usr/local/bin/harbor-relay-agent`
- `/etc/harbor-relay/agent.yaml`
- `/etc/harbor-relay/examples/agent.yaml.example`
- `/etc/systemd/system/harbor-relay-agent.service`

### 2. 编辑配置

```bash
sudo vi /etc/harbor-relay/agent.yaml
```

至少需要填：

- `relay_address`
- `relay_server_name`
- `site_name`
- `channels`
- `source_registry`
- `source_username`
- `source_password`
- `target_registry`
- `target_username`
- `target_password`

### 3. 激活服务

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run activate --role agent
```

### 4. 查看状态

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run status --role agent
systemctl status harbor-relay-agent --no-pager
```

## 配置文件建议

### relay 关键字段

- `http_listen`
  - Harbor webhook 和状态 API 的监听地址
- `grpc_listen`
  - agent 连接的 gRPC 监听地址
- `webhooks[].path`
  - Harbor webhook 入口路径
- `routes[].repository_patterns`
  - 仓库匹配规则
- `routes[].channel`
  - 路由到的逻辑频道
- `targets[].site_name`
  - 任务最终派发的站点
- `targets[].target_registry`
  - 目标仓库地址
- `targets[].target_project`
  - 目标项目改写

### agent 关键字段

- `relay_address`
  - relay 地址
- `relay_server_name`
  - TLS SNI 名称
- `site_name`
  - 当前 agent 所属站点
- `channels`
  - 当前 agent 消费哪些频道
- `docker_config_dir`
  - 独立的 Docker 凭据目录，避免污染 `/root/.docker/config.json`

## Caddy 配置建议

### Harbor

- 域名：`registry.example.com:9443`
- 反向代理到：
  - `127.0.0.1:8080`

### Relay

- 域名：`relay.example.com:9443`
- HTTP 请求转发到：
  - `127.0.0.1:18080`
- gRPC 请求转发到：
  - `h2c://127.0.0.1:19090`

参考：

- [relay.example.com.9443.caddy](../deploy/caddy/relay.example.com.9443.caddy)

### 文档站

- 域名：`docs.example.com:9443`
- 反向代理到：
  - `127.0.0.1:18081`

参考：

- [docs.example.com.9443.caddy](../deploy/caddy/docs.example.com.9443.caddy)

## 文档站部署建议

如果你准备把文档站挂到：

- `docs.image.hm.metavarse.tech:9443`

推荐使用挂载式部署，而不是每次改文档都重做运行镜像。

建议目录：

- 文档源码：
  - `/opt/release/src/00-utils/harbor-relay`
- 部署目录：
  - `/opt/harbor-relay-docs`
- 静态站点目录：
  - `/data/harbor-relay-docs/site`

推荐直接使用：

```bash
cd /opt/release/src/00-utils/harbor-relay/website/deploy
sudo ./install-docs-site.sh install \
  --repo-src /opt/release/src/00-utils/harbor-relay \
  --deploy-dir /opt/harbor-relay-docs \
  --data-dir /data/harbor-relay-docs \
  --domain docs.image.hm.metavarse.tech \
  --port 18081
```

后续改文档时，只需要：

```bash
sudo /opt/harbor-relay-docs/bin/rebuild-docs-site.sh
```

这样运行容器不需要重建，Caddy 继续反代：

- `127.0.0.1:18081`

## Harbor webhook 配置

建议每个业务项目单独一个 path。

例如：

```text
https://relay.example.com:9443/api/v1/harbor/webhook/team-a
```

同时在 relay 中配置：

```yaml
webhooks:
  - name: team-a
    path: /api/v1/harbor/webhook/team-a
    authorization: "Bearer xxx"
```

注意事项：

- 不要写双斜杠 `//`
- method 必须是 `POST`
- `Authorization` 要和 relay 配置一致

## 常用状态检查

```bash
curl http://127.0.0.1:18080/healthz
curl http://127.0.0.1:18080/api/v1/tasks
curl http://127.0.0.1:18080/api/v1/agents
curl http://127.0.0.1:18080/api/v1/notification-jobs
```

## 升级建议

### relay

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run install --role relay --force
sudo ./harbor-relay-toolkit-linux-amd64.run activate --role relay
```

### agent

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run install --role agent --force
sudo ./harbor-relay-toolkit-linux-amd64.run activate --role agent
```

## 卸载

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run uninstall --role relay
sudo ./harbor-relay-toolkit-linux-amd64.run uninstall --role agent
```

保留配置与状态：

- 默认保留

彻底清理：

```bash
sudo ./harbor-relay-toolkit-linux-amd64.run uninstall --role all --purge
```
