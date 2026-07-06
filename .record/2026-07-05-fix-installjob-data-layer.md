# 2026-07-05 installjob BC 跨层违规修复：拆 data 层 + 注册 Migrate

## 现象
ongrid 容器启动后反复打：
```
github.com/ongridio/ongrid/internal/manager/biz/installjob/repo.go:97
Error 1146 (42S02): Table 'ongrid.install_jobs' doesn't exist
```
worker 轮询每分钟触发一次 INSERT/SELECT 失败。list/get 也回 `record not found`。

## 全链路核对（依据项目规则"问题修复必须核对数据交互层"）

按五层顺序追：

| 层 | 路径 | 结论 |
|---|---|---|
| 1. 触发点 | `internal/manager/biz/installjob/repo.go:97`（`ListByDevice` / `Create` 都打到） | repo 持有 `gorm.DB` |
| 2. 业务层 | `internal/manager/biz/installjob/installer.go`、`worker.go`、`runner.go` 全部基于 `*InstallJob` 字段访问 | biz 与 data 混在一起 |
| 3. 路由/服务层 | `internal/manager/server/installjob/http.go` 通过 `installjobUsecaseAdapter{repo: installjobRepo,...}` 间接调用 | OK，server 走 adapter |
| 4. cmd wiring | `cmd/ongrid/main.go:947` `managerbizinstalljob.NewRepo(db)` | OK |
| 5. **数据交互层** | **不存在**：没有 `internal/manager/data/installjob/` 目录，**也没有 `db/migrations/` 目录** | **违规** |

## 根因（两条规则同时命中）

1. 「禁止跨层调用」（gospec 红线）：biz/installjob 直接持有 InstallJob + InstallJobEvent + gormRepo。注释里自己写过 "split it to internal/manager/data/installjob when a non-gorm backend joins the party"，承诺过拆分但跳过了。
2. 「全链路跑通」（项目规则第 3 条 + 业务规则第 5 条）：`dbx.RunMigrations(db, log, iamdatauser.Migrate, ..., managerflowdata.Migrate)` 列表里**没有** installjob 的 Migrate 函数 ⇒ AutoMigrate 跳过 ⇒ install_jobs / install_job_events 两表从未创建。

DB 现状（修复前 `SHOW TABLES`）：
51 张全在，唯独缺 `install_jobs` 和 `install_job_events` —— 两条规则互相印证：跨层 ⇒ data 层缺位 ⇒ 全链不通 ⇒ 1146。

## 改动（规范拆分版）

新增 `internal/manager/data/installjob/`：
- `model.go` — 持有 InstallJob / InstallJobEvent / Status / EventKind，gorm tag 完整保留（含 soft_delete.DeleteMarker、idx_install_jobs_device/status/deleted_at）
- `repo.go` — gormRepo + NewRepo + GormRepo（导出别名供 cmd 层编译期断言）
- `migrate.go` — `func Migrate(db *gorm.DB) error`，签名对齐 `dbx.Migrator`

改造 `internal/manager/biz/installjob/`：
- `model.go` —— 不再持有 InstallJob/InstallJobEvent 定义，改为 type alias 到 data 包（`type InstallJob = managerinstalldata.InstallJob`），保留 Status enum、EventKind enum、AuthKind 常量。
- `repo.go` —— 删除 gormRepo 实现，仅保留 Repo interface 与一行委托 `NewRepo(db) managerinstalldata.NewRepo(db)`。biz 不写 SQL、不持 *gorm.DB 实体。

`cmd/ongrid/main.go`：
- 导入 `managerinstalldata`
- `dbx.RunMigrations(...)` 末尾追加 `managerinstalldata.Migrate`（位置在 managerflowdata 之后，作为最后一个被注册 data 包）
- wiring 段加编译期断言：
  ```go
  var _ managerbizinstalljob.Repo = (*managerinstalldata.GormRepo)(nil)
  ```
  防止 Repo contract 演进时漏实现。

## 部署

按之前约定，从 Makefile 取构建命令：
```bash
VERSION=$(cat VERSION)
go build -trimpath -ldflags "-X main.version=$VERSION" \
  -o bin/ongrid ./cmd/ongrid
```

替换运行目录 binary：
```bash
cd /opt/ongrid
docker compose stop ongrid        # bind-mount 文件被持锁 → "文本文件忙"
cp -v bin/ongrid /opt/ongrid/ongrid-app/ongrid
docker compose start ongrid
```

## 验证

启动日志：
```
migration start index=21 total=21
SLOW SQL >= 200ms [769.774ms] CREATE TABLE `install_jobs` ...
SLOW SQL >= 200ms [598.637ms] CREATE TABLE `install_job_events` ...
migration done index=21 elapsed=1.376s
```

容器内 `healthz`：
```
$ docker exec ongrid curl :8080/healthz   →  ok
$ docker exec ongrid curl :8080/readyz    →  ready
```

数据库已建表：
```
mysql> SHOW TABLES LIKE 'install%';
install_job_events
install_jobs
```

最近 60 秒日志中 `1146` 出现次数：**0**。worker 轮询不再报错。

## 不再做的事（避免回归）

- 不要把 InstallJob 模型搬回 biz/installjob/model.go —— 历史注释里的承诺要兑现。
- 不要跳过 `managerinstalldata.Migrate` 注册 —— 缺它 installjob worker 会立即 1146。
- 不要去掉 cmd 层的 `var _ managerbizinstalljob.Repo = (*managerinstalldata.GormRepo)(nil)` 编译期断言 —— 后人改 Repo interface 时它第一个挡错误。
- 不要让其他新建 BC 走 installjob 这种"先跨层，后补 migation"的偷工路径；新建 BC 必须先在 `internal/manager/data/<bc>/` 占位，然后写 Migrate，注册到 main.go，最后才写 biz。
