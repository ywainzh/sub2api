# Sub2API 轻量生产部署手册

本手册适用于当前的阿里云服务器：`2 vCPU / 1.6 GiB RAM / 1 GiB Swap / x86_64`。服务器只拉取已发布镜像并运行 Docker Compose，绝不在服务器执行 Node、Go、pnpm、GoReleaser 或 Docker 构建。

| 项目 | 固定值 |
| --- | --- |
| 二开分支 | `release` |
| 镜像仓库 | `ghcr.io/ywainzh/sub2api` |
| 部署目录 | `/opt/sub2api` |
| 应用监听 | `127.0.0.1:39080` |
| 公网域名 | `https://sub2api.zyspeed.xyz` |
| 数据目录 | `/opt/sub2api/data`、`postgres_data`、`redis_data`、`opencode-mihomo` |
| 隔离验收地址 | `127.0.0.1:39081`，数据位于 `/opt/sub2api/test-data` |
| Docker socket GID | 服务器实际 `/var/run/docker.sock` 的 group id，当前服务器为 `121` |

## 版本规则

二开版本从 `v0.1.0` 开始，后续按补丁版本顺序递增：

```text
v0.1.0 -> v0.1.1 -> v0.1.2
```

- 每次发布必须使用全新的、不可复用的 `vX.Y.Z` tag。
- 日常二开、上游同步和小修复均递增最后一位，例如 `v0.1.1`。
- 只有在明确规划功能阶段时才升级中间位，例如 `v0.2.0`；不要使用日期、`latest`、`release-*` 或重推已有 tag。
- `release-2026.07.22-*` 是早期部署记录，仅用于审计和回滚参考；新版本一律使用 `vX.Y.Z`。

`Publish Lite Release` 只接受 `release` 分支最新提交上的 `vX.Y.Z` tag。它会执行前端类型检查和构建、后端全量测试、`linux/amd64` 编译、Compose 校验、GHCR 推送、匿名拉取校验和 GitHub Release 创建。后端代码有修改时仍必须重新编译一次 Go 二进制；前端构建、后端测试和二进制构建会在 GitHub Actions 中安全并行，通常约 5 分钟完成。服务器始终只拉取预编译镜像，不参与编译。上游的 `Release` 工作流已与本 fork 的版本 tag 隔离，不会再修改 `main`。

## 发布新版本

所有代码先进入 `release`，确认工作区干净后创建下一个版本。例如从 `v0.1.0` 发布 `v0.1.1`：

```bash
git switch release
git pull --ff-only origin release
git status
git log --oneline -10

git tag -a v0.1.1 -m "Sub2API v0.1.1"
git push origin v0.1.1
```

不要在 `main` 创建版本 tag。不要删除、重建或强制推送已发布 tag；发布失败时修复问题并创建下一个版本号。

在 GitHub Actions 中确认以下三条任务都成功，再操作服务器：

1. `Publish Lite Release`
2. `CI`
3. `Security Scan`

成功后 GitHub Release 会包含：

- `sub2api-linux-amd64.tar.gz`：预编译 Linux 二进制。
- `sub2api-deploy-vX.Y.Z.tar.gz`：Compose、Nginx、备份脚本和本手册。
- `checksums.txt`：发布包校验和。

服务器实际只使用部署包和 GHCR 镜像。镜像公开可拉取，服务器不保存 GitHub Token。

## 首次部署

### DNS 和基础条件

先创建 DNS A 记录：

```text
sub2api.zyspeed.xyz -> 47.251.82.144
```

确认服务器已安装 Docker、Docker Compose、Nginx、Certbot，并确保 `39080` 未被占用。在线更新需要应用容器访问宿主机 Docker socket，因此必须使用本手册中的 Compose 文件和 `DOCKER_GID`；PostgreSQL 和 Redis 不需要、也不应暴露到宿主机。

### 下载并校验部署包

以下示例以 `v0.1.0` 为例；实际部署时替换为本次 tag：

```bash
export SUB2API_TAG=v0.1.0
export SUB2API_DIR=/opt/sub2api
export SUB2API_RELEASE=https://github.com/ywainzh/sub2api/releases/download/${SUB2API_TAG}

sudo install -d -m 0750 "${SUB2API_DIR}"
curl -fsSL "${SUB2API_RELEASE}/sub2api-deploy-${SUB2API_TAG}.tar.gz" -o /tmp/sub2api-deploy.tar.gz
curl -fsSL "${SUB2API_RELEASE}/checksums.txt" -o /tmp/sub2api-checksums.txt

expected=$(grep "sub2api-deploy-${SUB2API_TAG}.tar.gz" /tmp/sub2api-checksums.txt | awk '{print $1}')
actual=$(sha256sum /tmp/sub2api-deploy.tar.gz | awk '{print $1}')
test -n "${expected}"
test "${expected}" = "${actual}"

tar -tzf /tmp/sub2api-deploy.tar.gz | grep -Eq '(^/|(^|/)\.\.(/|$))' && exit 1 || true
sudo tar -xzf /tmp/sub2api-deploy.tar.gz -C "${SUB2API_DIR}"
```

### 创建服务器专属配置

不要把 `.env`、管理员密码或任意密钥提交到 Git。首次部署时生成随机密钥：

```bash
cd /opt/sub2api
umask 077
sudo cp .env.example .env

postgres_password=$(openssl rand -hex 32)
redis_password=$(openssl rand -hex 32)
jwt_secret=$(openssl rand -hex 32)
totp_key=$(openssl rand -hex 32)
admin_password=$(openssl rand -hex 16)

sudo sed -i "s#^POSTGRES_PASSWORD=.*#POSTGRES_PASSWORD=${postgres_password}#" .env
sudo sed -i "s#^REDIS_PASSWORD=.*#REDIS_PASSWORD=${redis_password}#" .env
sudo sed -i "s#^JWT_SECRET=.*#JWT_SECRET=${jwt_secret}#" .env
sudo sed -i "s#^TOTP_ENCRYPTION_KEY=.*#TOTP_ENCRYPTION_KEY=${totp_key}#" .env
sudo sed -i "s#^ADMIN_PASSWORD=.*#ADMIN_PASSWORD=${admin_password}#" .env
unset postgres_password redis_password jwt_secret totp_key admin_password

sudo chmod 600 .env
sudo mkdir -p data postgres_data redis_data backups
docker_gid=$(stat -c '%g' /var/run/docker.sock)
sudo sed -i "s#^DOCKER_GID=.*#DOCKER_GID=${docker_gid}#" .env
unset docker_gid
grep -q ':latest$' .env && echo 'APP_IMAGE must use a fixed version' >&2 && exit 1 || true
docker compose config --quiet
```

发布包中的 `.env.example` 已自动写入本次镜像，例如：

```text
APP_IMAGE=ghcr.io/ywainzh/sub2api:v0.1.0
```

管理员账号默认为 `admin@sub2api.local`。初始密码仅保存在 `/opt/sub2api/.env`，登录后立即在后台修改；不要将它发送到聊天、工单或仓库。

`ADMIN_EMAIL` 和 `ADMIN_PASSWORD` 只在空数据库首次自动初始化管理员时使用。管理员账号已创建后，修改 `.env` 不会覆盖数据库中已有账号的邮箱或密码；应使用实际管理员账号登录，或通过应用的正常找回/管理流程处理凭据，切勿为此删除数据目录或重置数据库。

### 启动容器

```bash
cd /opt/sub2api
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=200 sub2api
curl -fsS http://127.0.0.1:39080/health
```

预期 `sub2api`、`sub2api-postgres`、`sub2api-redis` 都是 `healthy`，`opencode-mihomo` 为 `running`。Mihomo Controller 和各节点 listener 只存在于 Docker 内网，不映射任何公网端口。

资源上限已写入 Compose：应用 `384 MiB / 1 CPU`、Mihomo `256 MiB / 0.5 CPU`、PostgreSQL `256 MiB / 0.5 CPU`、Redis `128 MiB / 0.25 CPU`。PostgreSQL、Redis 和 Mihomo 仅在 Docker 内网监听，应用仅绑定 `127.0.0.1:39080`。

## OpenCode 自动 Worker 池

OpenCode 客户端始终使用统一入口：

```text
Base URL: https://sub2api.zyspeed.xyz/v1
API Key: 已在后台绑定 OpenCode 池的 Sub2API Key
```

OpenCode Zen 上游 Key 与客户端使用的 Sub2API Key 是两层凭据。池保持 Keyless 时，上游 Key 留空，Worker 不发送 `Authorization`；客户端仍必须发送自己的 Sub2API Key。生产验收使用 API Key #3“测试国产模型”。

管理员配置顺序：

1. 在“代理管理 → OpenCode”确认订阅已同步，并对节点执行探测。
2. 只有 `healthy`、非 429、出口 IP 唯一且探测时间不超过 15 分钟的节点会创建 Worker。
3. 在池设置中保持启用，按需启用“服务器直连”，然后点击“立即对账”。服务器直连会采用最早的现有 `server_direct` OpenCode 账号。
4. 在用户的 API Key 弹框中打开“追加 OpenCode”。生产只给 API Key #3 打开，Key #1、#2 保持不变。
5. 请求免费模型时自动切到系统池；非免费模型继续使用 Key 原分组。免费模型命中但池无 Worker 时返回 `503 opencode_pool_unavailable`，不会回落普通账号或服务器直连。

基础验证：

```bash
curl -fsS https://sub2api.zyspeed.xyz/v1/models \
  -H 'Authorization: Bearer <Sub2API API Key #3>'

curl -fsS https://sub2api.zyspeed.xyz/v1/chat/completions \
  -H 'Authorization: Bearer <Sub2API API Key #3>' \
  -H 'Content-Type: application/json' \
  -d '{"model":"big-pickle","messages":[{"role":"user","content":"Reply OK"}],"stream":false}'
```

免费 OpenCode 请求仍保存账号、Worker、节点和 token 用量记录，但 `actual_cost=0`，不扣 API Key 金额配额。429 会立即换 Worker，并按上游 reset 时间或默认 60 秒冷却当前 Worker。

## `39081` 隔离验收栈

`docker-compose.test.yml` 使用独立 PostgreSQL、Redis、Mihomo 和数据目录，不挂载 Docker socket，也不连接生产 Docker 网络。它把服务器现有 `/opt/proxy-service/config.yaml` 只读挂载到内部 seed 服务，供测试后台以 HTTP 订阅导入；生产栈不能访问该 seed 服务。

部署发布镜像后创建测试配置：

```bash
cd /opt/sub2api
test -f /opt/proxy-service/config.yaml
umask 077
cp .env.test.example .env.test

test_postgres_password=$(openssl rand -hex 32)
test_redis_password=$(openssl rand -hex 32)
test_jwt_secret=$(openssl rand -hex 32)
test_totp_key=$(openssl rand -hex 32)
test_admin_password=$(openssl rand -hex 16)
test_mihomo_secret=$(openssl rand -hex 32)

sed -i "s#^TEST_POSTGRES_PASSWORD=.*#TEST_POSTGRES_PASSWORD=${test_postgres_password}#" .env.test
sed -i "s#^TEST_REDIS_PASSWORD=.*#TEST_REDIS_PASSWORD=${test_redis_password}#" .env.test
sed -i "s#^TEST_JWT_SECRET=.*#TEST_JWT_SECRET=${test_jwt_secret}#" .env.test
sed -i "s#^TEST_TOTP_ENCRYPTION_KEY=.*#TEST_TOTP_ENCRYPTION_KEY=${test_totp_key}#" .env.test
sed -i "s#^TEST_ADMIN_PASSWORD=.*#TEST_ADMIN_PASSWORD=${test_admin_password}#" .env.test
sed -i "s#^TEST_OPENCODE_MIHOMO_SECRET=.*#TEST_OPENCODE_MIHOMO_SECRET=${test_mihomo_secret}#" .env.test
unset test_postgres_password test_redis_password test_jwt_secret test_totp_key test_admin_password test_mihomo_secret

chmod 600 .env.test
mkdir -p test-data/app test-data/postgres test-data/redis test-data/opencode-mihomo
cp -n opencode-mihomo/config.yaml test-data/opencode-mihomo/config.yaml

docker compose --env-file .env.test -f docker-compose.test.yml config --quiet
docker compose --env-file .env.test -f docker-compose.test.yml pull
docker compose --env-file .env.test -f docker-compose.test.yml up -d
docker compose --env-file .env.test -f docker-compose.test.yml ps
curl -fsS http://127.0.0.1:39081/health
```

登录隔离后台后，添加订阅地址 `http://opencode-seed/config.yaml`，依次执行同步、全量探测和 Worker 对账。至少验证两个 listener 并发出口、代理故障只换 Worker、无隐式直连，以及 Sub2API/Mihomo 重启后的 Worker 和租约恢复。

验收完成后停止隔离栈；该操作不触碰生产容器和生产数据：

```bash
cd /opt/sub2api
docker compose --env-file .env.test -f docker-compose.test.yml down
```

## Nginx 和 HTTPS

部署包中的 `nginx-sub2api.conf` 已代理到本机 `39080`，禁用响应缓冲并支持 WebSocket、SSE 和长连接。

```bash
cd /opt/sub2api
sudo install -m 0644 nginx-sub2api.conf /etc/nginx/sites-available/sub2api.zyspeed.xyz.conf
sudo ln -s /etc/nginx/sites-available/sub2api.zyspeed.xyz.conf /etc/nginx/sites-enabled/sub2api.zyspeed.xyz.conf
sudo nginx -t
sudo systemctl reload nginx

sudo certbot --nginx -d sub2api.zyspeed.xyz --redirect
curl -fsS https://sub2api.zyspeed.xyz/health
```

Certbot 会自动配置续期。HTTP 应返回 301 到 HTTPS；HTTPS 健康检查应返回：

```json
{"status":"ok"}
```

如果 Nginx 刚 reload 时短暂返回 404，先验证直连和 Host 路由，再重试：

```bash
curl -fsS http://127.0.0.1:39080/health
curl -fsS -H 'Host: sub2api.zyspeed.xyz' http://127.0.0.1/health
```

## 升级到新版本

升级不会重新构建，不会删除数据目录。当前 Compose 已包含 Docker socket 挂载，版本菜单会直接执行在线更新。以 `v0.1.1` 为例：

升级 OpenCode Worker 池版本前，先备份数据库、环境文件和 Compose，并轮换 Mihomo Controller secret：

```bash
cd /opt/sub2api
./backup.sh
cp .env "backups/.env.$(date +%Y%m%d-%H%M%S)"
cp docker-compose.yml "backups/docker-compose.$(date +%Y%m%d-%H%M%S).yml"

opencode_mihomo_secret=$(openssl rand -hex 32)
sed -i "s#^OPENCODE_MIHOMO_SECRET=.*#OPENCODE_MIHOMO_SECRET=${opencode_mihomo_secret}#" .env
unset opencode_mihomo_secret
chmod 600 .env
```

旧 Controller secret 不再继续使用。升级后必须同时确认 `sub2api` 与 `opencode-mihomo` 使用新值并正常通信。

1. 在管理后台右上角版本菜单点击“立即更新”。
2. 后端校验最新版本必须是固定的 `vX.Y.Z`，拉取 `ghcr.io/ywainzh/sub2api:v0.1.1`。
3. 独立更新助手先执行 PostgreSQL 备份，再原子修改 `.env`，只重建 `sub2api` 容器；PostgreSQL、Redis 和数据目录不重建。
4. 页面等待目标版本健康检查通过后自动刷新。

更新日志写入 `/opt/sub2api/backups/update-*.log`，数据库备份写入同目录。更新助手失败会恢复更新前的 `.env` 和旧镜像配置，并尝试恢复旧容器。

如果版本菜单提示 Docker 在线更新未配置，说明服务器仍使用旧部署包，先按“旧部署迁移”完成一次迁移，再使用页面更新。

### 旧部署迁移

已运行的 `v0.1.3` 或更早版本不会自动获得 socket 挂载。停机窗口内下载最新部署包，保留现有 `.env`、`data/`、`postgres_data/`、`redis_data/` 和 `backups/`，只覆盖 Compose、脚本和文档：

```bash
cd /opt/sub2api
./backup.sh
cp .env "backups/.env.$(date +%Y%m%d-%H%M%S)"

export SUB2API_TAG=v0.1.5
curl -fsSL "https://github.com/ywainzh/sub2api/releases/download/${SUB2API_TAG}/sub2api-deploy-${SUB2API_TAG}.tar.gz" -o /tmp/sub2api-deploy.tar.gz
mkdir -p /tmp/sub2api-deploy
tar -xzf /tmp/sub2api-deploy.tar.gz -C /tmp/sub2api-deploy
cp /tmp/sub2api-deploy/docker-compose.yml /opt/sub2api/docker-compose.yml
cp /tmp/sub2api-deploy/backup.sh /opt/sub2api/backup.sh
chmod 750 /opt/sub2api/backup.sh
docker_gid=$(stat -c '%g' /var/run/docker.sock)
if grep -q '^DOCKER_GID=' .env; then
    sed -i "s#^DOCKER_GID=.*#DOCKER_GID=${docker_gid}#" .env
else
    printf '\nDOCKER_GID=%s\n' "$docker_gid" >> .env
fi
unset docker_gid
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=200 sub2api
curl -fsS https://sub2api.zyspeed.xyz/health
```

发布包可用于更新 Compose、Nginx 或备份脚本；解压前保留现有 `.env`、`data/`、`postgres_data/`、`redis_data/` 和 `backups/`，不要用发布包覆盖这些目录。上面的 `cp` 只覆盖配置模板和脚本，不会触碰业务数据。

升级成功后只清理旧的 `ghcr.io/ywainzh/sub2api:*` 镜像，保留当前和上一版。不要运行 `docker system prune -a`，因为它可能影响同机的其他项目。

## 回滚

在管理后台版本菜单展开“版本回退”，选择最近的固定版本即可在线回滚。Docker 回滚同样先拉取目标 GHCR 镜像，再由助手切换 `.env` 并重建 `sub2api`；不会回滚数据库内容。

从 `v0.2.0` 回滚到 `v0.1.20` 前，先在后台解绑 API Key #3，停用 OpenCode 池并执行一次对账。对账会停用系统 Worker并释放出口租约，然后再切换旧镜像。迁移 195 新增的表可以保留，旧版普通 OpenAI 账号不使用这些表。

服务器无法访问后台时，也可以手工回滚镜像：

```bash
cd /opt/sub2api
sed -i 's#^APP_IMAGE=.*#APP_IMAGE=ghcr.io/ywainzh/sub2api:v0.1.0#' .env
docker compose pull
docker compose up -d
docker compose ps
curl -fsS https://sub2api.zyspeed.xyz/health
```

若新版本包含不兼容数据库迁移，先停止服务，将当前数据库目录移到带时间戳的可恢复目录，恢复升级前备份，再启动旧镜像：

```bash
cd /opt/sub2api
docker compose down
mv postgres_data "postgres_data.failed-$(date +%Y%m%d-%H%M%S)"
mkdir postgres_data
docker compose up -d postgres
until docker compose exec -T postgres pg_isready -U sub2api -d sub2api; do sleep 2; done
gunzip -c backups/sub2api.<timestamp>.sql.gz | docker compose exec -T postgres psql -U sub2api -d sub2api
docker compose up -d
```

只有确认迁移不兼容时才恢复数据库备份；普通程序回滚不要恢复数据库。

## 备份、监控和排障

`backup.sh` 每次执行都会创建 PostgreSQL 压缩备份，并自动删除超过 7 天的备份。安装每日任务：

```bash
cd /opt/sub2api
chmod 750 backup.sh
sudo install -m 0644 cron-sub2api-backup /etc/cron.d/sub2api-backup
./backup.sh
```

任务在 `Asia/Shanghai` 每天 `03:15` 运行。Redis AOF 只保存缓存和运行状态，不能替代 PostgreSQL 备份。

日常检查：

```bash
cd /opt/sub2api
docker compose ps
docker compose logs --tail=200 sub2api
docker stats --no-stream sub2api sub2api-postgres sub2api-redis
free -h
df -h /
curl -fsS https://sub2api.zyspeed.xyz/health
```

故障处理顺序：

1. `docker compose ps` 确认应用、PostgreSQL、Redis 健康，Mihomo 正常运行。
2. 查看 `docker compose logs --tail=200 sub2api`。
3. 确认 `.env` 存在、权限为 `600`，且 `APP_IMAGE` 不是 `latest`。
4. 检查 `free -h`、`docker stats` 和 `df -h /`；服务器内存紧张时不要在服务器构建镜像。
5. 检查 HTTPS 健康接口、Nginx 配置和证书状态。
6. 需要恢复时优先回滚镜像；只有数据库迁移问题才恢复数据库备份。

## 上游同步和发布边界

`main` 只同步 `upstream/main`，二开代码和版本 tag 只进入 `release`。完整分支同步流程见仓库根目录的 `BRANCHING.md`。上游同步后先合并到 `release`、解决冲突、通过验证，再按本手册发布新的 `vX.Y.Z` 版本。
