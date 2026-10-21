# 部署说明（Jenkins → 练习机）

公开仓库不写真实 IP、密码或 Token。部署机地址只放在 Jenkins 全局环境变量 `DEPLOY_HOST` 里。

伴暖目前是进程内内存存储，没有数据库。流水线不启动 Postgres。

## 拓扑

```text
浏览器 ──:80──> nginx-gw（宿主机网络）
                 ├─ /tiancode      → 301 /tiancode/
                 └─ /tiancode/     → 127.0.0.1:8092（去掉 /tiancode 前缀）
                                      └─ 容器 :8000  伴暖 API + prototype 静态页
Jenkins ──SSH deploy──> 宿主 /opt/tiancode
                         docker compose -p tiancode up -d
```

公开路径是 `http://<HOST>/tiancode/`（原型首页）、`http://<HOST>/tiancode/admin.html`（管理原型）、`http://<HOST>/tiancode/docs`（Swagger）。API 为 `http://<HOST>/tiancode/api/v1/...`。

容器只绑定 `127.0.0.1:8092`，不对公网直接开放。8090/8091 已被 agent-memory 占用。

## 前缀

nginx 使用 `proxy_pass http://127.0.0.1:8092/;`（末尾斜杠），把 `/tiancode/` 剥掉后再交给应用。应用路由仍是 `/readyz`、`/`、`/admin.html`。

镜像启动命令是 `uvicorn app.main:app --root-path "$ROOT_PATH"`。`ROOT_PATH=/tiancode` 只修正 OpenAPI / Swagger 的对外地址。应用不会再挂一层 `/tiancode`，也不会把前缀剥第二次。`X-Forwarded-Prefix` 与 `ROOT_PATH` 保持一致。当前 uvicorn 不会用这个头再改一次 `root_path`。

## 凭据（只写 ID）

| ID | 类型 | 内容 |
|---|---|---|
| `ghcr-token` | 用户名 + 密码 | 与 agent-memory 共用。GitHub 用户名 + 仅 `write:packages` 的 PAT |
| `deploy-ssh` | SSH 私钥 | 与 agent-memory 共用。部署机 `deploy` 用户 |
| `tiancode-deploy-env` | Secret file | **新建**。`/opt/tiancode/.env` 全文，按 `deploy/.env.example` 填真值 |

不要把 agent-memory 的 `deploy-env` 拷给伴暖。那份文件是 Postgres 密码和 `API_PORT=8090`，套过来会和 memory 抢端口，也会把无关密钥灌进这个进程。

Jenkins 全局环境变量沿用现有的 `DEPLOY_HOST` 与 `DEPLOY_USER=deploy`。

镜像：`ghcr.io/zhangqi77ok-sys/tiancode`。目录：`/opt/tiancode`。Compose 项目名：`tiancode`。

## 流水线

| 阶段 | 做什么 |
|---|---|
| Init | 分支等于参数 `DEPLOY_BRANCH`（默认 `main`）才部署。`TAG=<提交前 12 位>-b<构建号>` |
| Secrets scan | gitleaks 扫仓库 |
| Lint & Test | `pip install -e '.[dev]'`、`ruff check backend`、`pytest`。没有 Postgres 阶段 |
| Build image | 所有分支都 `docker build` |
| Push GHCR | 仅部署分支。凭据缺失或过期则失败，不会静默改用别的镜像 |
| Dedup check | 同一提交已部署且 `/readyz` 为 200 时跳过。`FORCE_DEPLOY` 可强制重来 |
| Deploy | `scp` compose 与 `.env`，`docker compose -p tiancode up -d` |
| Smoke | 部署机上 `curl 127.0.0.1:$API_PORT$SMOKE_PATH`（默认 `/readyz`）。默认路径时再检查 `/` 与 `/admin.html`。失败则回滚到 `.last_tag` |

## `.env`

模板见 `deploy/.env.example`。

| 变量 | 含义 |
|---|---|
| `JWT_SECRET` | 签发 JWT。`openssl rand -hex 32`。缺失或仍是 `change-me` / `change-me-openssl-rand-hex-32` / `tiancode-family-secret-32-bytes-min!` 时进程拒绝启动 |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | 管理登录。`ADMIN_PASSWORD` 至少 12 位。`change-me` 和 `AdminPassword123!` 会拒绝启动 |
| `API_PORT` | 宿主端口，默认 `8092`，前面固定 `127.0.0.1` |
| `ROOT_PATH` | `/tiancode` |

改密钥时同时改 Jenkins 凭据 `tiancode-deploy-env`。下次部署会用凭据覆盖部署机上的 `.env`。

## nginx

片段：`deploy/nginx/gateway-tiancode.conf.example`。合并进 nginx-gw 的 server 后执行：

```bash
docker restart nginx-gw
```

不要用 `nginx -s reload` 代替这次重启。网关配置是单文件 bind mount 时，reload 仍可能看着旧 inode。重启期间 `/jenkins/` 也会短暂不可用。

## 本地检查

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -e '.[dev]'
pytest -q
docker build -t tiancode:dev .
docker run --rm -d --name tiancode-dev -p 127.0.0.1:8092:8000 \
  -e JWT_SECRET="$(openssl rand -hex 32)" -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD="$(openssl rand -base64 24)" \
  -e ROOT_PATH=/tiancode tiancode:dev
curl -sf http://127.0.0.1:8092/healthz
curl -sf http://127.0.0.1:8092/readyz
docker rm -f tiancode-dev
```

## 回滚

冒烟失败时流水线读取 `/opt/tiancode/.last_tag` 并重新 `compose up`。没有上一次成功记录时不会猜 `latest`。

手动：

```bash
cd /opt/tiancode && cat .last_tag
# 例：ghcr.io/zhangqi77ok-sys/tiancode:<tag>
IMAGE=ghcr.io/zhangqi77ok-sys/tiancode TAG=<tag> docker compose -p tiancode up -d
curl -sf 127.0.0.1:8092/readyz
```
