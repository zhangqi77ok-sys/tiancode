# 部署说明（Jenkins → 练习机）

公开仓库不写真实 IP、密码或 Token。部署机地址只放在 Jenkins 全局环境变量 `DEPLOY_HOST` 里。

家庭、成员、设置、插件开关、插件进度、登录失败计数和注销名单都在 PostgreSQL。compose 里的服务名是 `postgres`（镜像 `postgres:16-alpine`），数据在卷 `tiancode-pg`。5432 不映射到宿主，避免和同机别的库抢端口。重启容器不会清空卷。清空：`docker compose -p tiancode down` 之后删除卷 `tiancode_tiancode-pg`。

以前的 `TIANCODE_DB` SQLite 文件不再读取，也不会被导入。那份文件和旧卷 `tiancode_tiancode-data` 可以删掉。表结构在 `backend/app/core/schema.sql`，API 进程启动时执行 `CREATE TABLE IF NOT EXISTS`。没有单独的 Alembic 版本链。

这台练习机的公开地址是 HTTP。令牌会经过这条链路。在 nginx 前面加上 TLS 之前，不要把它当成公网生产入口。uvicorn 的 `--forwarded-allow-ips` 默认是 `127.0.0.1`，不再使用 `*`。容器里看到的对端通常是 Docker 网桥而不是 127.0.0.1，所以默认不会采信 `X-Forwarded-*`。只有在 TLS 终结的那一跳地址写进 `FORWARDED_ALLOW_IPS` 时才放行。

## 拓扑

```text
浏览器 ──:80──> nginx-gw（宿主机网络）
                 ├─ /tiancode      → 301 /tiancode/
                 └─ /tiancode/     → 127.0.0.1:8092（去掉 /tiancode 前缀）
                                      └─ 容器 api:8000  伴暖 API + prototype 静态页
                                           └─ 容器 postgres:5432（不映射到宿主）
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
| Lint & Test | 起一个一次性 `postgres:16-alpine`（`127.0.0.1:55432`），`pip install -e '.[dev]'`、`ruff check backend`、`TEST_DATABASE_URL=... pytest`。测完删掉这个容器。不会连部署库 |
| Build image | 所有分支都 `docker build` |
| Push GHCR | 仅部署分支。凭据缺失或过期则失败，不会静默改用别的镜像 |
| Dedup check | 同一提交已部署且 `/readyz` 为 200 时跳过。`FORCE_DEPLOY` 可强制重来 |
| Deploy | `scp` compose 与 `.env`，`docker compose -p tiancode up -d` |
| Smoke | 部署机执行 `deploy/smoke.sh`。默认 `/readyz` 就绪后检查 `/`、`/login.html`、`/admin.html`，匿名管理接口为 401，注册一个守护者后 `/auth/me` 为 200，该令牌访问 `/api/v1/admin/dashboard/stats` 为 403。`SMOKE_PATH` 不是 `/readyz` 时只探测那一条路径。失败则回滚到 `.last_tag` |

## `.env`

模板见 `deploy/.env.example`。

| 变量 | 含义 |
|---|---|
| `JWT_SECRET` | 签发 JWT。`openssl rand -hex 32`。缺失或仍是 `change-me` / `change-me-openssl-rand-hex-32` / `tiancode-family-secret-32-bytes-min!` 时进程拒绝启动 |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | 管理登录。`ADMIN_PASSWORD` 至少 12 位。`change-me` 和 `AdminPassword123!` 会拒绝启动 |
| `API_PORT` | 宿主端口，默认 `8092`，前面固定 `127.0.0.1` |
| `ROOT_PATH` | `/tiancode` |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | compose 里的 PostgreSQL。口令用 `openssl rand -hex 24`。`change-me-pg-password` 会让 API 拒绝启动 |
| `TIANCODE_DATABASE_URL` | `postgresql://...`。compose 用服务名 `postgres` 覆盖 `.env` 里给宿主机 uvicorn 的地址。也接受 `DATABASE_URL`。不接受 `sqlite:`，也不再读 `TIANCODE_DB` |
| `FORWARDED_ALLOW_IPS` | 允许设置转发头的地址，默认 `127.0.0.1`。不要写 `*` |

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
docker run -d --name tiancode-pytest-pg \
  -e POSTGRES_USER=tiancode -e POSTGRES_PASSWORD=tiancode_test_pw \
  -e POSTGRES_DB=tiancode_test -p 127.0.0.1:55432:5432 postgres:16-alpine
export TEST_DATABASE_URL=postgresql://tiancode:tiancode_test_pw@127.0.0.1:55432/tiancode_test
pytest -q
docker rm -f tiancode-pytest-pg

# 练习机同款：在 deploy/ 放好替换过占位符的 .env，再启动 api 和 postgres。
docker build -t tiancode:dev .
cd deploy && IMAGE=tiancode TAG=dev docker compose -p tiancode up -d
curl -sf http://127.0.0.1:8092/healthz
curl -sf http://127.0.0.1:8092/readyz
docker compose -p tiancode down
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
