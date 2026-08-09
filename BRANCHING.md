# 分支管理说明

本仓库基于官方项目 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 进行二次开发。为便于持续同步官方更新，长期维护以下分支和远程仓库。

## 分支与远程职责

| 名称 | 用途 |
| --- | --- |
| `upstream/main` | 官方项目的主分支，只作为上游更新来源。 |
| `origin/main` | 本仓库的上游镜像分支，应与 `upstream/main` 保持一致。 |
| `release` | 本仓库的长期二次开发和发布分支，所有定制修改均在此分支进行。 |

不要直接在 `main` 上进行二次开发，也不要对已经推送的 `release` 执行变基或强制推送。上游更新统一通过合并进入 `release`，以保留可追踪的同步历史。

## 日常二次开发

开始修改前，确认当前位于 `release` 并更新本地分支：

```bash
git switch release
git pull --ff-only origin release
git status
```

完成修改后，在 `release` 上正常提交并推送：

```bash
git add <files>
git commit -m "<message>"
git push origin release
```

## 版本发布

二开版本只从 `release` 创建，采用不可复用的语义化 tag：`v0.1.0`、`v0.1.1`、`v0.1.2`。不要使用日期 tag 或 `latest`，也不要在 `main` 创建发布 tag。

Agent 必须按 [发布与部署 Runbook](deploy/lite/AGENT_RUNBOOK.md) 的固定顺序执行。首次部署和运维参考见 [轻量生产部署手册](deploy/lite/README.md)。发布前必须确认 GitHub Actions 的 `Publish Lite Release`、`CI` 和 `Security Scan` 均成功。

## 同步官方上游

同步前必须提交或暂存当前修改，确保 `git status` 显示工作区干净。然后先更新镜像分支 `main`：

```bash
git switch main
git fetch upstream
git merge --ff-only upstream/main
git push origin main
```

再将更新后的 `main` 合并到二次开发分支：

```bash
git switch release
git pull --ff-only origin release
git merge main
git push origin release
```

`main` 必须使用 `--ff-only` 更新。如果该命令失败，说明 `main` 已存在本地或仓库自有提交，应先检查历史，不要强制覆盖：

```bash
git log --oneline --graph --decorate --all -30
```

## 处理合并冲突

合并 `main` 到 `release` 时如果发生冲突：

1. 使用 `git status` 查看冲突文件。
2. 根据二次开发需求和上游变化修改冲突内容。
3. 使用 `git add <files>` 标记冲突已解决。
4. 使用 `git commit` 完成合并提交，再运行 `git push origin release`。

如果暂时无法确认正确处理方式，可使用 `git merge --abort` 放弃本次合并，仓库会恢复到合并前的状态。
