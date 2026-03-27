# ClawManager OpenClaw Image

<p align="center">
  <img src="docs//assets/openclaw_logo.jpg" alt="ClawManager" width="100%" />
</p>

<p align="center">
  <strong>Languages:</strong>
  <a href="./README.md">English</a> |
  <a href="./README.zh-CN.md">中文</a>
</p>

本指南帮助您为 **ClawManager** 控制面构建 **OpenClaw** 基础镜像，支持 **自动化配置注入**（API Key、Base URL 等）与多租户场景下的 **持久化目录布局**。

**推荐优先使用 `Dockerfile.openclaw` 一键构建。** 若需在 WebTop 桌面内手动安装组件后再执行 `docker commit`，请见下文 **进阶：基础流程（手动 / docker commit）**。


## 项目概览

在 ClawManager 批量管理场景下，逐台手动配置容器不可扩展。本项目提供：

* **预装运行环境**：Node.js 与最新 OpenClaw CLI，开箱即用。
* **配置同步**：通过 `custom-cont-init.d`，新容器启动时从 `/defaults` 恢复模板。
* **动态注入**：用环境变量更新 `openclaw.json`，无需在桌面会话里手改文件。

---

## 快速开始（推荐）

镜像基于 `lscr.io/linuxserver/webtop:ubuntu-xfce`：Dockerfile 会安装 Node.js 与全局 OpenClaw，写入 `/defaults/.openclaw`，并将 `scripts/99-openclaw-sync` 安装到 `/custom-cont-init.d/`（在**每次容器启动**时执行，而非 `docker build` 时）。运行时配置位于 **`/config/.openclaw`**，而非 `~/.openclaw`。启动后可在容器日志中查找 `[OpenClaw]` 相关输出。

### 构建

**Bash**

```
docker build -f Dockerfile.openclaw -t openclaw:local .
```

### 运行

> **共享内存**：运行 WebTop 时必须传入 `--shm-size="1gb"`（至少 1GB），否则浏览器/桌面栈可能崩溃或行为异常。

**Bash**

```
docker run -d \
  --name=webtop-openclaw \
  --shm-size="1gb" \
  --restart unless-stopped \
  -e PUID=1000 \
  -e PGID=1000 \
  -e TZ=Asia/Shanghai \
  -e CLAWMANAGER_LLM_BASE_URL=https://your-gateway/v1 \
  -e CLAWMANAGER_LLM_API_KEY=your-sk-key \
  -e CLAWMANAGER_LLM_MODEL=gpt-4o \
  -p 3000:3000 \
  -p 3001:3001 \
  openclaw:local
```

按需调整端口与占位符。

---

## 环境变量

在 ClawManager 或 `docker run` 中设置以下变量，以写入 `openclaw.json`：

| 变量                         | 配置路径                                 | 说明                                                                      |
| ---------------------------- | ---------------------------------------- | ------------------------------------------------------------------------- |
| `CLAWMANAGER_LLM_BASE_URL` | `models.providers.auto.baseUrl`        | 网关或上游 Base URL                                                       |
| `CLAWMANAGER_LLM_API_KEY`  | `apiKey`                               | 模型 API 密钥                                                             |
| `CLAWMANAGER_LLM_MODEL`    | `primary` / `agents.defaults.models` | 模型 ID 替换；`auto/` 的处理与 `99-openclaw-sync` 中 `sed` 逻辑一致 |

---

## GitHub Actions 与 GHCR（可选）

工作流 [`.github/workflows/docker-ghcr.yml`](.github/workflows/docker-ghcr.yml) 会在推送到默认分支（`main` / `master`）或推送 `v*` 标签时构建 `Dockerfile.openclaw`，并推送到 **GitHub Container Registry**，发布时无需在本地执行 `docker build`。

**简要步骤**

1. 将仓库推送到 GitHub，在 **Actions** 中确认 **Build and push to GHCR** 成功。
2. 在 **Packages** 中查找镜像，地址一般为 `ghcr.io/<user>/<repo>`。
3. 私有包需先执行 `docker login ghcr.io`；若希望匿名 `docker pull`，请将 Package 设为 **Public**。

**Bash**

```
docker pull ghcr.io/<github_user>/<repo>:latest

docker run -d \
  --name=webtop-openclaw \
  --shm-size="1gb" \
  --restart unless-stopped \
  -e PUID=1000 -e PGID=1000 -e TZ=Asia/Shanghai \
  -e CLAWMANAGER_LLM_BASE_URL=https://your-gateway/v1 \
  -e CLAWMANAGER_LLM_API_KEY=your-sk-key \
  -e CLAWMANAGER_LLM_MODEL=gpt-4o \
  -p 3000:3000 -p 3001:3001 \
  ghcr.io/<github_user>/<repo>:latest
```

推送如 `v1.0.0` 等标签时，工作流还会按 metadata 规则发布语义化版本标签。

---

## 进阶：基础流程（`docker commit`）

仅在需要在保存镜像前于 WebTop 内安装额外工具时使用；与 `Dockerfile.openclaw` 二选一。

### 安装软件

在浏览器中打开 `https://<IP>:3001`，在终端中执行：

**Bash**

```
curl -fsSL https://deb.nodesource.com/setup_current.x | sudo -E bash -
sudo apt-get install -y nodejs

npm config set registry https://registry.npmmirror.com
sudo npm install -g openclaw@latest
```

### 初始化脚本与清理

1. **写入默认模板**：`cp -rp /config/.openclaw /defaults/`。
2. **安装钩子**：将可执行的 `99-openclaw-sync` 放到 `/custom-cont-init.d/`（可从本仓库的 `scripts/99-openclaw-sync` 起步），负责从 `/defaults` 同步到 `/config` 并按环境变量修改。
3. **保存镜像前清理**：`rm -rf /config/.openclaw`。若跳过此步，新容器可能无法按预期执行首次启动初始化。

### 保存镜像

**Bash**

```
docker commit webtop-running openclaw:v1.0
```

---

## 说明

* **行尾**：`99-openclaw-sync` 须使用 **LF**。在 Windows 上请转换行尾，或依赖 `Dockerfile.openclaw` 中的 `sed` 去除 `\r`。修改脚本后请重新构建镜像。
* **权限**：脚本会执行 `chown -R abc:abc`，保证默认用户可读写持久化配置。
* **Docker Compose**：将 `image` 指向您构建的标签，或使用 `build` 且指定 `dockerfile: Dockerfile.openclaw`。不要仅依赖官方 `webtop` 镜像，其中不包含本仓库模板与初始化脚本。
* **单独使用 WebTop**：若不使用 ClawManager 批量能力，可跳过进阶流程中与 ClawManager 相关的步骤。

---

## 链接

* [ClawManager - 管理AI代理最友好的方式](https://github.com/Yuan-lab-LLM/ClawManager)
