# ClawManager OpenClaw Image

<p align="center">
  <img src="docs//assets/openclaw_logo.jpg" alt="ClawManager" width="100%" />
</p>

<p align="center">
  <strong>Languages:</strong>
  <a href="./README.md">English</a> |
  <a href="./README.zh-CN.md">中文</a>
</p>

This guide helps you build an **OpenClaw** base image for the **ClawManager** control plane, with **automated config injection** (API Key, Base URL, etc.) and **persistent directory layout** for multi-tenant scenarios.

**Prefer `Dockerfile.openclaw` for a one-shot build.** If you need to install components manually inside the WebTop desktop and then `docker commit`, see **Advanced: manual flow** below.


## Project overview

In ClawManager batch scenarios, per-container manual setup does not scale. This project addresses:

* **Pre-installed runtime**: Node.js and the latest OpenClaw CLI, ready to use.
* **Config sync**: `custom-cont-init.d` so new containers can restore templates from `/defaults` on start.
* **Dynamic injection**: Environment variables update `openclaw.json` without editing files in the desktop session.

---

## Quick start (recommended)

The image is based on `lscr.io/linuxserver/webtop:ubuntu-xfce`. The Dockerfile installs Node.js and global OpenClaw, seeds `/defaults/.openclaw`, and installs `scripts/99-openclaw-sync` under `/custom-cont-init.d/` (runs on **each container start**, not during `docker build`). Runtime config lives under **`/config/.openclaw`**, not `~/.openclaw`. Look for `[OpenClaw]` lines in the container logs after startup.

### Build

**Bash**

```
docker build -f Dockerfile.openclaw -t openclaw:local .
```

### Run

> **Set shared memory**: always pass `--shm-size="1gb"` (at least 1GB) when running WebTop, or the browser/desktop stack may crash or behave oddly.

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

Adjust ports and placeholders as needed.

---

## Environment variables

Set these in ClawManager or `docker run` to inject into `openclaw.json`:

| Variable                     | Config path                              | Purpose                                                                                   |
| ---------------------------- | ---------------------------------------- | ----------------------------------------------------------------------------------------- |
| `CLAWMANAGER_LLM_BASE_URL` | `models.providers.auto.baseUrl`        | Gateway or upstream base URL                                                              |
| `CLAWMANAGER_LLM_API_KEY`  | `apiKey`                               | Model API key                                                                             |
| `CLAWMANAGER_LLM_MODEL`    | `primary` / `agents.defaults.models` | Model id replacement;`auto/` handling matches the `sed` logic in `99-openclaw-sync` |

---

## GitHub Actions and GHCR (optional)

The workflow [`.github/workflows/docker-ghcr.yml`](.github/workflows/docker-ghcr.yml) builds `Dockerfile.openclaw` on push to the default branch (`main` / `master`) or on `v*` tags, and pushes to **GitHub Container Registry** so you do not need a local `docker build` for releases.

**Short checklist**

1. Push the repo to GitHub and confirm **Build and push to GHCR** succeeds under **Actions**.
2. Find the package under **Packages**; the image is usually `ghcr.io/<user>/<repo>`.
3. For private packages, run `docker login ghcr.io` first; set the package to **Public** if you want anonymous `docker pull`.

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

Pushing tags like `v1.0.0` also publishes semver tags per the workflow metadata rules.

---

## Advanced: manual flow (`docker commit`)

Use this when you must install extra tooling inside WebTop before saving an image. It is an alternative to `Dockerfile.openclaw`.

### Install software

Open `https://<IP>:3001`, then in a terminal:

**Bash**

```
curl -fsSL https://deb.nodesource.com/setup_current.x | sudo -E bash -
sudo apt-get install -y nodejs

npm config set registry https://registry.npmmirror.com
sudo npm install -g openclaw@latest
```

### Init script and cleanup

1. **Seed defaults**: `cp -rp /config/.openclaw /defaults/`.
2. **Install hook**: place an executable `99-openclaw-sync` under `/custom-cont-init.d/` (you can start from `scripts/99-openclaw-sync` in this repo) to copy from `/defaults` to `/config` and apply env-based edits.
3. **Clean before image save**: `rm -rf /config/.openclaw`. If this step is skipped, new containers may not run first-boot init as expected.

### Save image

**Bash**

```
docker commit webtop-running openclaw:v1.0
```

---

## Notes

* **Line endings**: `99-openclaw-sync` must use **LF**. On Windows, convert line endings or rely on the `sed` step in `Dockerfile.openclaw` to strip `\r`. Rebuild the image after editing the script.
* **Permissions**: the script runs `chown -R abc:abc` so the default user can read/write persisted config.
* **Docker Compose**: point `image` at your built tag, or use `build` with `dockerfile: Dockerfile.openclaw`. Do not rely on the stock `webtop` image alone; it will not include this repo’s templates and init script.
* **Standalone WebTop**: if you do not use ClawManager batch features, you can skip the ClawManager-specific steps in the advanced flow.

---

## Links

* [ClawManager - The friendliest way to manage Al agents](https://github.com/Yuan-lab-LLM/ClawManager)
