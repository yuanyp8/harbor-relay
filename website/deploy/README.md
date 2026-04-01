# 文档站部署

这套部署方式的目标是：

- 运行时使用一个轻量的静态文件容器
- 文档内容和静态产物都挂载到宿主机
- 你随时可以直接修改源文档，再手工重建
- Caddy 只负责外部 HTTPS 和反向代理
- systemd 负责容器开机自启和服务托管

## 推荐目录

- 源码目录
  - 例如 `/opt/release/src/00-utils/harbor-relay`
- 部署目录
  - 例如 `/opt/harbor-relay-docs`
- 数据目录
  - 例如 `/data/harbor-relay-docs`

数据目录会包含：

- `/data/harbor-relay-docs/site`
  - Docusaurus 构建后的静态站点
- `/data/harbor-relay-docs/npm-cache`
  - Node 构建缓存

## 一键安装

```bash
cd /opt/release/src/00-utils/harbor-relay/website/deploy
chmod +x ./install-docs-site.sh

sudo ./install-docs-site.sh install \
  --repo-src /opt/release/src/00-utils/harbor-relay \
  --deploy-dir /opt/harbor-relay-docs \
  --data-dir /data/harbor-relay-docs \
  --domain docs.image.hm.metavarse.tech \
  --port 18081
```

安装完成后：

- `harbor-relay-docs.service` 会被启用
- 本机监听 `127.0.0.1:18081`
- Caddy 可以反代到这个端口

## 日常更新文档

### 1. 修改文档源文件

通常改这些位置：

- `docs/*.md`
- `website/src/pages/index.tsx`
- `website/docusaurus.config.ts`

### 2. 重新构建静态站点

```bash
sudo /opt/harbor-relay-docs/bin/rebuild-docs-site.sh
```

### 3. 容器无需重建

因为运行容器直接挂载的是：

- `/data/harbor-relay-docs/site`

所以只要重建静态文件，网页内容就会更新。

## 查看状态

```bash
sudo /opt/harbor-relay-docs/bin/install-docs-site.sh status
systemctl status harbor-relay-docs --no-pager
docker ps --filter name=harbor-relay-docs
```

## Caddy

安装脚本会打印对应的 Caddy 配置建议。

如果你的文档域名是：

- `docs.image.hm.metavarse.tech:9443`

对应站点可参考：

- `deploy/caddy/docs.image.hm.metavarse.tech.9443.caddy`

## 适合这种模式的原因

这套方式比“每次改文档都重新构建运行镜像”更适合你当前场景，因为：

- 文档改动频繁
- 运行容器不需要包含源码
- 静态站点可以直接挂在宿主机上备份和查看
- 运维人员能独立执行重建
