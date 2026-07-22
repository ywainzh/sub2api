# Sub2API 轻量部署

此部署方案面向 2 核、2 GiB 以内内存的 x86_64 Linux 服务器。服务器只拉取固定版本镜像并运行容器，不执行 Node、Go 或 Docker 镜像构建。

## 发布约定

- 发布来源只能是 `release` 分支。
- 发布 tag 格式为 `release-YYYY.MM.DD-N`。
- GitHub Actions 构建 `linux/amd64` 镜像并发布到 `ghcr.io/ywainzh/sub2api:<tag>`。
- 生产环境禁止使用 `latest`。

发布示例：

```bash
git switch release
git pull --ff-only origin release
git status
git tag -a release-2026.07.22-1 -m "Sub2API lite release 2026.07.22-1"
git push origin release-2026.07.22-1
```

等待 `Publish Lite Release` 工作流成功，并确认对应 GitHub Release 和 GHCR 镜像均已生成后，才能部署。

## 首次部署

DNS 必须先添加以下 A 记录：

```text
sub2api.zyspeed.xyz -> 47.251.82.144
```

在服务器下载 GitHub Release 中的部署包并解压到 `/opt/sub2api`，然后准备配置：

```bash
cd /opt/sub2api
umask 077
cp .env.example .env

postgres_password="$(openssl rand -hex 32)"
redis_password="$(openssl rand -hex 32)"
jwt_secret="$(openssl rand -hex 32)"
totp_key="$(openssl rand -hex 32)"
admin_password="$(openssl rand -hex 16)"

sed -i "s#^POSTGRES_PASSWORD=.*#POSTGRES_PASSWORD=${postgres_password}#" .env
sed -i "s#^REDIS_PASSWORD=.*#REDIS_PASSWORD=${redis_password}#" .env
sed -i "s#^JWT_SECRET=.*#JWT_SECRET=${jwt_secret}#" .env
sed -i "s#^TOTP_ENCRYPTION_KEY=.*#TOTP_ENCRYPTION_KEY=${totp_key}#" .env
sed -i "s#^ADMIN_PASSWORD=.*#ADMIN_PASSWORD=${admin_password}#" .env
unset postgres_password redis_password jwt_secret totp_key admin_password

chmod 600 .env
mkdir -p data postgres_data redis_data backups
grep -q ':latest$' .env && echo 'ERROR: APP_IMAGE must use a fixed tag' >&2 && exit 1 || true
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=200 sub2api
curl -fsS http://127.0.0.1:39080/health
```

管理员初始密码保存在服务器的 `/opt/sub2api/.env` 中，不应复制到仓库、发布附件或聊天记录。

## Nginx 与 HTTPS

DNS 生效后安装站点配置并申请证书：

```bash
sudo cp nginx-sub2api.conf /etc/nginx/sites-available/sub2api.zyspeed.xyz.conf
sudo ln -s /etc/nginx/sites-available/sub2api.zyspeed.xyz.conf /etc/nginx/sites-enabled/sub2api.zyspeed.xyz.conf
sudo nginx -t
sudo systemctl reload nginx
sudo certbot --nginx -d sub2api.zyspeed.xyz
```

最终通过 `https://sub2api.zyspeed.xyz/health` 验证 HTTPS、反向代理和应用健康状态。

## 升级与回滚

升级前先备份数据库和配置：

```bash
cd /opt/sub2api
cp .env "backups/.env.$(date +%Y%m%d-%H%M%S)"
./backup.sh
```

将 `.env` 中的 `APP_IMAGE` 改为新的固定 tag 后执行：

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=200 sub2api
curl -fsS http://127.0.0.1:39080/health
```

回滚时将 `APP_IMAGE` 改回上一版固定 tag，再执行相同的 `pull` 和 `up -d`。数据库迁移不兼容时，应停止容器并恢复升级前的 PostgreSQL 备份。

只保留当前和上一版 Sub2API 镜像。禁止使用 `docker system prune -a`，也不要删除其他项目镜像。

## 运行检查

```bash
cd /opt/sub2api
docker compose ps
docker compose logs --tail=200 sub2api
docker stats --no-stream sub2api sub2api-postgres sub2api-redis
free -h
df -h /
curl -fsS http://127.0.0.1:39080/health
```

PostgreSQL 是业务数据的唯一权威存储。`backup.sh` 会创建压缩的 `pg_dump` 并删除超过 7 天的数据库备份。首次部署时安装定时任务：

```bash
chmod 750 /opt/sub2api/backup.sh
sudo cp /opt/sub2api/cron-sub2api-backup /etc/cron.d/sub2api-backup
sudo chmod 644 /etc/cron.d/sub2api-backup
```

Redis AOF 用于恢复缓存和运行状态，但不能替代 PostgreSQL 备份。
