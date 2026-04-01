# Harbor Relay

`harbor-relay` 用来承接这条链路：

`Harbor Webhook -> Relay -> 远端 Agent -> 目标仓库 -> 回调`

它解决的是“一个 Harbor，多个远端 DC，多类镜像按频道分发”的问题。

## 核心能力

- 一个 relay 同时承接多个 Harbor 项目的 webhook
- webhook 先按仓库规则映射成 `channel`
- 支持通过 `webhook_names` 控制“某个 subpath 只命中某些 route”
- 再按 `target_sites` 映射成具体远端任务
- 远端 agent 只订阅自己关心的 `channel`
- agent 通过 gRPC 长连接取任务
- agent 用 Docker 执行 `pull / tag / push`
- relay 统一记录状态，并可选触发回调

## 为什么要拆成独立仓库

这套能力已经不再是简单的 `00-utils` 脚本了，而是一套完整服务：

- 有自己的 Go 模块
- 有自己的配置模型
- 有自己的 systemd / Caddy 接入方式
- 有自己的测试和构建

所以把它单独拆成仓库后，维护边界会更清楚。

## 目录结构

- `cmd/relay`
  - relay 服务入口
- `cmd/agent`
  - 远端 agent 入口
- `internal/config`
  - 配置模型与默认值
- `internal/relay`
  - webhook 接收、任务展开、状态存储、gRPC 派发
- `internal/agent`
  - docker pull / tag / push 执行器
- `proto/`
  - gRPC 协议
- `gen/`
  - protobuf 生成代码
- `configs/`
  - 配置样例
- `deploy/systemd`
  - systemd 服务文件
- `deploy/caddy`
  - Caddy 站点样例

## 快速理解代码

如果你想最快掌握代码，建议按这个顺序看：

1. [configs/relay.yaml.example](./configs/relay.yaml.example)
2. [configs/agent.yaml.example](./configs/agent.yaml.example)
3. [proto/relay/v1/relay.proto](./proto/relay/v1/relay.proto)
4. [cmd/relay/main.go](./cmd/relay/main.go)
5. [internal/relay/service.go](./internal/relay/service.go)
6. [internal/relay/store.go](./internal/relay/store.go)
7. [internal/relay/grpc.go](./internal/relay/grpc.go)
8. [internal/agent/agent.go](./internal/agent/agent.go)
9. [ARCHITECTURE.md](./ARCHITECTURE.md)

## 本地测试

这次我专门补了“更容易看懂系统行为”的单元测试，重点覆盖两类能力：

1. Harbor webhook 是否会正确展开成任务
2. route 是否会把 repository 正确映射成 channel
3. agent 订阅 channel 时，store 是否只会派发匹配任务
4. webhook subpath 是否会按配置命中对应 route
5. HTTP API、gRPC 派单、store 状态流转是否正常

直接运行：

```bash
go test ./...
```

如果你只想先看 webhook 这条链路：

```bash
go test ./internal/relay -run Webhook -v
```

如果你想重点看 gRPC 派单：

```bash
go test ./internal/relay -run GRPC -v
```

## 构建

```bash
./build.sh amd64
./build.sh arm64
```

产物：

- `dist/linux-amd64/harbor-relay`
- `dist/linux-amd64/harbor-relay-agent`
- `dist/linux-arm64/harbor-relay`
- `dist/linux-arm64/harbor-relay-agent`

## Relay 部署

1. 准备配置

```bash
mkdir -p /etc/harbor-relay /data/harbor-relay
cp configs/relay.yaml.example /etc/harbor-relay/relay.yaml
```

如果 Harbor 是通过局域网或 Caddy 反向代理来打 webhook，记得把：

```yaml
http_listen: 0.0.0.0:18080
```

否则只监听 `127.0.0.1` 时，Harbor 是打不进来的。

2. 构建二进制

```bash
./build.sh amd64
```

3. 安装二进制与 systemd

```bash
install -m 0755 dist/linux-amd64/harbor-relay /usr/local/bin/harbor-relay
install -m 0644 deploy/systemd/harbor-relay.service /etc/systemd/system/harbor-relay.service
systemctl daemon-reload
systemctl enable --now harbor-relay
```

## Agent 部署

1. 准备配置

```bash
mkdir -p /etc/harbor-relay /data/harbor-relay-agent
cp configs/agent.yaml.example /etc/harbor-relay/agent.yaml
```

2. 安装

```bash
install -m 0755 dist/linux-amd64/harbor-relay-agent /usr/local/bin/harbor-relay-agent
install -m 0644 deploy/systemd/harbor-relay-agent.service /etc/systemd/system/harbor-relay-agent.service
systemctl daemon-reload
systemctl enable --now harbor-relay-agent
```

## Caddy 接入

Relay 可以继续复用 `9443`，只要域名和 Harbor 不同即可：

- `image.hm.metavarse.tech:9443`
  - Harbor
- `relay.hm.metavarse.tech:9443`
  - Relay

因为 Caddy 会按 `Host/SNI` 和 `Content-Type` 分流：

- gRPC
  - 转给 `h2c://127.0.0.1:19090`
- 普通 HTTP
  - 转给 `127.0.0.1:18080`

参考文件：

- [deploy/caddy/relay.hm.metavarse.tech.9443.caddy](./deploy/caddy/relay.hm.metavarse.tech.9443.caddy)

## 验收命令

Relay 本地健康检查：

```bash
curl http://127.0.0.1:18080/healthz
```

通过 Caddy 验证入口：

```bash
curl -kI --resolve relay.hm.metavarse.tech:9443:127.0.0.1 https://relay.hm.metavarse.tech:9443/healthz
```

查看 agent 和任务：

```bash
curl http://127.0.0.1:18080/api/v1/agents
curl http://127.0.0.1:18080/api/v1/tasks
```

## 同机测试 source / target 项目

如果你想在同一台 Harbor 服务器上测试“源项目推送 -> relay -> 目标项目同步”，推荐这样配：

```yaml
targets:
  - name: yunnan-mid
    site_name: yunnan-mid
    target_registry: image.hm.metavarse.tech:9443
    target_project: yunnan-mid-test
```

这样当源仓库是：

```text
yunnan-mid/registry-photon
```

目标仓库就会被改写成：

```text
yunnan-mid-test/registry-photon
```

这比单纯用 `repository_prefix` 更适合“项目级改写”的场景。

## 可观测性说明

现在 relay 默认会把这些过程都打印到标准输出：

- `/healthz` 请求
- `/api/v1/tasks`、`/api/v1/agents` 请求
- 每一次 webhook 接收
- webhook 鉴权失败、重复事件、无匹配路由、入队成功
- route 命中 / 跳过原因
- gRPC agent hello / heartbeat / 断开
- 任务派发
- agent progress
- callback 调用结果

也就是说，只要 Harbor webhook 真正打进 relay，不管成功还是失败，日志里都会留下处理轨迹。

## GitHub Actions

仓库已经适合直接接 CI，后面建议默认至少保留：

- `go test ./...`
- `./build.sh amd64`

这样 webhook 路由、channel 调度这类逻辑变更时，更容易第一时间发现回归。
