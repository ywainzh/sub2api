# Sub2API Agent 发布与部署 Runbook

本文件是 Agent 执行 `release` 分支发布、阿里云生产升级和回滚的唯一顺序。首次安装、Nginx 和日常运维细节见 [README.md](README.md)。

## 1. 固定目标

| 项目 | 固定值 |
| --- | --- |
| 仓库 | `github.com/ywainzh/sub2api` |
| 发布分支 | `release` |
| 镜像 | `ghcr.io/ywainzh/sub2api:vX.Y.Z` |
| SSH | `aliyun-server` |
| 生产目录 | `/opt/sub2api` |
| 生产健康检查 | `http://127.0.0.1:39080/health` |
| 公网健康检查 | `https://sub2api.zyspeed.xyz/health` |
| 隔离验收 | `http://127.0.0.1:39081/health` |

硬性规则：

- 修改代码不等于授权发布。用户未明确要求“提交、发布或部署”时，完成测试后停止。
- 服务器只拉取发布镜像，禁止在服务器编译 Go、Node 或 Docker 镜像。
- 生产只使用不可变的 `vX.Y.Z`，禁止 `latest`、复用 tag、强推或删除已发布 tag。
- 不输出、记录或提交 Token、API Key、订阅 URL、代理凭据、数据库密码和 Controller secret。
- 不暂存无关文件；禁止 `git reset --hard`、`git checkout --`、`docker system prune -a` 和未经批准的数据库恢复。
- 任一步验收失败立即停止后续步骤；先保留日志和现场，再修复或回滚。

## 2. 发布权限与变更分级

开始前确认用户授权范围：

| 用户授权 | Agent 可以执行 |
| --- | --- |
| 仅修改/修复 | 编辑和测试；不提交、不推送、不发布 |
| 提交/推送 | 提交并推送 `release`；不创建 tag、不部署 |
| 发布 | 推送新 tag并等待全部工作流成功；不自动部署 |
| 部署 | 完成备份、部署、验收；失败时按本文件回滚 |

决定是否运行 `39081` 隔离栈：

- 仅前端或文档：不要求隔离栈。
- 后端、迁移、Compose、OpenCode 调度/代理/认证：生产前必须通过隔离栈。
- 删除字段、改写大量生产数据或不可逆迁移：停止并取得单独批准，同时准备数据库恢复演练。

## 3. 本地发布前检查

在仓库根目录执行：

```bash
git switch release
git status --short
git diff --check
git log -1 --oneline
git remote -v
```

先识别并保留用户已有修改。只按实际改动执行对应验证：

```bash
# 后端或迁移有改动
cd backend
go test ./...
go vet ./...
cd ..

# 前端有改动
cd frontend
pnpm run typecheck
pnpm run lint:check
pnpm run test:run
pnpm run build
cd ..

# Compose 有改动；只注入一次性测试值，禁止读取生产 .env
env APP_IMAGE=ghcr.io/ywainzh/sub2api:v0.0.0-test DOCKER_GID=121 \
  POSTGRES_PASSWORD=test REDIS_PASSWORD=test ADMIN_PASSWORD=test \
  JWT_SECRET=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  TOTP_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  OPENCODE_MIHOMO_SECRET=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  docker compose -f deploy/lite/docker-compose.yml config --quiet

env TEST_APP_IMAGE=ghcr.io/ywainzh/sub2api:v0.0.0-test \
  TEST_POSTGRES_PASSWORD=test TEST_REDIS_PASSWORD=test TEST_ADMIN_PASSWORD=test \
  TEST_JWT_SECRET=1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  TEST_TOTP_ENCRYPTION_KEY=1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  TEST_OPENCODE_MIHOMO_SECRET=1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
  docker compose -f deploy/lite/docker-compose.test.yml config --quiet

git diff --check
```

允许用定向测试替代完整前端测试的前提：完整测试已知存在与本次无关的基线失败，并在交付中准确记录；CI 实际覆盖的测试仍必须通过。迁移文件一旦发布不得修改，只能新增迁移。

## 4. 提交、推送和创建版本

仅暂存本次文件，检查后提交：

```bash
git add <本次文件>
git diff --cached --check
git diff --cached --stat
git commit -m "<type>: <summary>"
git status --short
```

推送前再次确认远端所有者为 `ywainzh`。认证必须遵守仓库根目录 `AGENTS.md`：优先 `GH_TOKEN_YWAINZH`，后备 `GITHUB_TOKEN_YWAINZH`，仅通过单次 Git extraheader 注入；不得改写 remote URL 或显示 Token。

```bash
git push origin release
git fetch origin release --tags
git rev-parse HEAD
git rev-parse origin/release
git tag --sort=-v:refname --list 'v[0-9]*' | head
```

确认本地与远端提交一致，并由用户确认新版本号后创建全新 tag：

```bash
export SUB2API_TAG=vX.Y.Z
git tag -a "${SUB2API_TAG}" -m "Sub2API ${SUB2API_TAG}"
git push origin "${SUB2API_TAG}"
```

Tag 推送后不得重建。发布失败时修复代码并使用下一个版本号。

## 5. CI 发布门禁

服务器操作前，等待该提交和 tag 触发的所有相关任务结束。以下三类必须全部成功：

1. `Publish Lite Release`
2. `CI`
3. `Security Scan`

同时确认：

- GitHub Release 已生成；
- `sub2api-deploy-${SUB2API_TAG}.tar.gz` 和 `checksums.txt` 存在；
- `ghcr.io/ywainzh/sub2api:${SUB2API_TAG}` 可匿名读取；
- Release 源提交等于 `release` 当前提交。

任何任务失败都不得部署。

## 6. 下载并校验发布包

发布包解压到固定版本目录，后续步骤即使重新连接 SSH 也能继续。

```bash
ssh aliyun-server
set -eu
target_tag=vX.Y.Z
target_image="ghcr.io/ywainzh/sub2api:${target_tag}"
deploy_dir=/opt/sub2api
release_url="https://github.com/ywainzh/sub2api/releases/download/${target_tag}"
release_stage="${deploy_dir}/releases/${target_tag}"
mkdir -p "${release_stage}/files"

curl -fsSL "${release_url}/sub2api-deploy-${target_tag}.tar.gz" -o "${release_stage}/deploy.tar.gz"
curl -fsSL "${release_url}/checksums.txt" -o "${release_stage}/checksums.txt"
expected="$(grep "sub2api-deploy-${target_tag}.tar.gz" "${release_stage}/checksums.txt" | awk '{print $1}')"
actual="$(sha256sum "${release_stage}/deploy.tar.gz" | awk '{print $1}')"
test -n "${expected}"
test "${expected}" = "${actual}"
tar -tzf "${release_stage}/deploy.tar.gz" | grep -Eq '(^/|(^|/)\.\.(/|$))' && exit 1 || true
tar -xzf "${release_stage}/deploy.tar.gz" -C "${release_stage}/files"
```

## 7. 高风险版本的隔离验收

后端、迁移、Compose 或 OpenCode 相关版本先部署到独立 `39081` 栈。测试栈不得复用生产 `.env`、PostgreSQL、Redis、Mihomo 目录或 Docker socket。

```bash
target_tag=vX.Y.Z
target_image="ghcr.io/ywainzh/sub2api:${target_tag}"
release_stage="/opt/sub2api/releases/${target_tag}"
cd /opt/sub2api
install -m 0644 "${release_stage}/files/docker-compose.test.yml" docker-compose.test.yml
cp "${release_stage}/files/.env.test.example" .env.test
grep -Fxq "TEST_APP_IMAGE=${target_image}" .env.test
test -f /opt/proxy-service/config.yaml
umask 077

test_postgres_password="$(openssl rand -hex 32)"
test_redis_password="$(openssl rand -hex 32)"
test_jwt_secret="$(openssl rand -hex 32)"
test_totp_key="$(openssl rand -hex 32)"
test_admin_password="$(openssl rand -hex 16)"
test_mihomo_secret="$(openssl rand -hex 32)"
sed -i "s#^TEST_POSTGRES_PASSWORD=.*#TEST_POSTGRES_PASSWORD=${test_postgres_password}#" .env.test
sed -i "s#^TEST_REDIS_PASSWORD=.*#TEST_REDIS_PASSWORD=${test_redis_password}#" .env.test
sed -i "s#^TEST_JWT_SECRET=.*#TEST_JWT_SECRET=${test_jwt_secret}#" .env.test
sed -i "s#^TEST_TOTP_ENCRYPTION_KEY=.*#TEST_TOTP_ENCRYPTION_KEY=${test_totp_key}#" .env.test
sed -i "s#^TEST_ADMIN_PASSWORD=.*#TEST_ADMIN_PASSWORD=${test_admin_password}#" .env.test
sed -i "s#^TEST_OPENCODE_MIHOMO_SECRET=.*#TEST_OPENCODE_MIHOMO_SECRET=${test_mihomo_secret}#" .env.test
unset test_postgres_password test_redis_password test_jwt_secret test_totp_key test_admin_password test_mihomo_secret
chmod 600 .env.test

docker compose --env-file .env.test -f docker-compose.test.yml down
test_timestamp="$(date +%Y%m%d-%H%M%S)"
if [ -d test-data ]; then mv test-data "test-data.previous-${test_timestamp}"; fi
mkdir -p test-data/app test-data/postgres test-data/redis test-data/opencode-mihomo
cp "${release_stage}/files/opencode-mihomo/config.yaml" test-data/opencode-mihomo/config.yaml

docker compose --env-file .env.test -f docker-compose.test.yml config --quiet
docker compose --env-file .env.test -f docker-compose.test.yml pull
docker compose --env-file .env.test -f docker-compose.test.yml up -d
docker compose --env-file .env.test -f docker-compose.test.yml ps
test_ready=0
for _ in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:39081/health >/dev/null 2>&1; then test_ready=1; break; fi
  sleep 2
done
test "${test_ready}" = 1
```

最低验收：迁移成功、容器重启后恢复、目标接口通过、代理故障不直连。OpenCode 变更还需验证 Chat、Responses、Messages、不同出口 Worker 和租约唯一。完成后停止测试栈：

```bash
docker compose --env-file .env.test -f docker-compose.test.yml down
```

## 8. 生产备份与部署

### 8.1 备份

```bash
target_tag=vX.Y.Z
target_image="ghcr.io/ywainzh/sub2api:${target_tag}"
deploy_dir=/opt/sub2api
release_stage="${deploy_dir}/releases/${target_tag}"
cd "${deploy_dir}"
timestamp="$(date +%Y%m%d-%H%M%S)"
snapshot_dir="backups/pre-${target_tag}-${timestamp}"
mkdir -p "${snapshot_dir}"
cp .env docker-compose.yml "${snapshot_dir}/"
./backup.sh
latest_db_backup="$(ls -1t backups/sub2api.*.sql.gz | head -1)"
test -s "${latest_db_backup}"
gzip -t "${latest_db_backup}"
```

备份任一步失败必须停止。记录 `snapshot_dir`、数据库备份文件和当前镜像，用于回滚；不要输出 `.env` 内容。

### 8.2 更新部署文件与镜像

仅覆盖部署模板和脚本，绝不覆盖 `.env`、数据目录或备份目录：

```bash
target_tag=vX.Y.Z
target_image="ghcr.io/ywainzh/sub2api:${target_tag}"
release_stage="/opt/sub2api/releases/${target_tag}"
install -m 0644 "${release_stage}/files/docker-compose.yml" docker-compose.yml
install -m 0644 "${release_stage}/files/docker-compose.test.yml" docker-compose.test.yml
install -m 0644 "${release_stage}/files/.env.test.example" .env.test.example
install -m 0750 "${release_stage}/files/backup.sh" backup.sh

sed -i "s#^APP_IMAGE=.*#APP_IMAGE=${target_image}#" .env
grep -q '^APP_IMAGE=.*:latest$' .env && exit 1 || true
docker compose config --quiet
docker compose pull
docker compose up -d
```

如果新版 `.env.example` 增加必填变量，先补齐生产 `.env` 再执行 `up`。只有 OpenCode Controller/边车配置变更或 secret 泄露时才轮换 `OPENCODE_MIHOMO_SECRET`；轮换后必须同时重建 `sub2api` 和 `opencode-mihomo`。

数据库迁移由应用启动时自动执行并写入 `schema_migrations`，禁止手工重复执行迁移 SQL。

## 9. 生产验收

按顺序执行，全部通过才算完成：

```bash
cd /opt/sub2api
docker compose ps
docker compose logs --tail=200 sub2api
production_ready=0
for _ in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:39080/health >/dev/null 2>&1; then production_ready=1; break; fi
  sleep 2
done
test "${production_ready}" = 1
curl -fsS https://sub2api.zyspeed.xyz/health
docker inspect -f '{{.Name}} {{.Config.Image}} restart={{.RestartCount}}' sub2api
docker stats --no-stream sub2api sub2api-postgres sub2api-redis opencode-mihomo
```

再检查：

1. 实际镜像 tag 等于目标版本，`sub2api`、PostgreSQL、Redis 为 healthy，Mihomo 正常运行。
2. 启动日志没有 migration、database、Redis、panic 或 Mihomo Controller 错误。
3. `schema_migrations` 包含本版本新增迁移。
4. 前端改动通过目标页面人工检查；后端改动执行对应真实接口冒烟。
5. OpenCode 改动使用指定测试 Key 验证 `/v1/models`、Chat、Responses 和 Messages；只报告 HTTP 状态与非敏感摘要。
6. 确认普通 Key 和普通 OpenAI 分组未发生行为变化。

不要为了验收创建或删除未授权的生产账号、Key、订阅或节点。

## 10. 失败与回滚

健康检查、迁移或核心请求失败时，先保存日志，然后恢复升级前镜像和 Compose：

```bash
cd /opt/sub2api
snapshot_dir=/opt/sub2api/backups/pre-vX.Y.Z-YYYYMMDD-HHMMSS
cp "${snapshot_dir}/.env" .env
cp "${snapshot_dir}/docker-compose.yml" docker-compose.yml
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
curl -fsS http://127.0.0.1:39080/health
```

- 普通回滚不恢复数据库；增量迁移可以保留。
- 只有确认旧程序无法兼容新迁移，并获得用户明确批准后，才停止服务并恢复数据库备份。
- 回滚也失败时停止操作，不进行清库、重建数据目录或连续试错。

## 11. 交付报告

最终报告只包含：

- 版本 tag、commit、镜像；
- 三类 CI 状态；
- 隔离验收是否执行及结果；
- 生产备份路径；
- 容器、迁移、健康检查和真实请求结果；
- 是否回滚及当前实际版本；
- 已知非阻断警告。

不得包含任何 secret、完整订阅 URL、代理认证信息或 API Key。
