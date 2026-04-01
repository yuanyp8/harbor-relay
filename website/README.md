# Docusaurus 文档站

这个目录是 `harbor-relay` 的公开文档站工程。

它基于 Docusaurus 构建，文档源文件来自：

- `../docs`

## 本地预览

```bash
cd website
npm install
npm run start
```

默认本地预览地址：

- `http://127.0.0.1:3000`

## 生产构建

```bash
cd website
npm install
npm run build
```

构建产物会输出到：

- `website/build/`

## 容器方式

当前推荐的生产部署方式不是“把文档内容打死在运行镜像里”，而是：

- 宿主机保留文档源码
- 宿主机保留构建后的静态站点
- 运行容器只负责提供静态文件

这样你修改文档后，只需要重建静态站点，不需要重做运行镜像。

## 推荐部署入口

请优先使用：

- [deploy/README.md](./deploy/README.md)
- [deploy/install-docs-site.sh](./deploy/install-docs-site.sh)

这套方式会提供：

- 宿主机挂载的静态站点目录
- `systemd` 托管的 docs 容器
- 一键重建脚本
- 可直接接入 Caddy 的本地监听端口

## 如果你仍然想构建一个包含静态站点的镜像

仓库仍然保留了当前 Dockerfile，可用于打包静态文档镜像：

```bash
docker build -t harbor-relay-docs:latest -f website/Dockerfile .
```

但对于“文档经常改、希望宿主机直接挂载内容”的场景，更推荐 `deploy/` 下的挂载式部署方案。
