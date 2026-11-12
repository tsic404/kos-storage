# kos-storage

KOS 系统应用项目：存储视图 — 卷与阶段、Provider 与存储类、使用率、备份状态与最近演练、迁移入口。

- 设计：`tsic/kos-design` 仓的 `kos-storage/`（范围、契约、验收、决策记录）

## 边界

- 只读存储对象与指标，发起迁移与删除前确认。
- 卷的创建、扩容、删除保护与迁移由 kosd 执行；本项目不创建或删除 PVC。
- 不读写卷内数据（`KOSD-NG-10`），不实现存储后端、CSI 或 RAID/ZFS。

## 模块

| 包 | 职责 |
|---|---|
| `pkg/storage` | 存储页声明与读数：设备事实、存储类与 Provider（`STORAGE-C-82`、`KOSD-C-47/48`）、卷与阶段（`KOSD-C-38`）、使用率（指标源未接入时不可测量）、备份状态与最近演练（`KOSD-C-41`）、删除入口（`KOSD-C-39`）、迁移入口与阶段/回滚结果（`STORAGE-C-81`、`KOSD-C-40`） |

页面按 `KOSD-C-53` 标注每项来源类型与来源；只读项给出只读原因。删除与迁移入口按会话角色与所有权裁剪：Owner 可写全部，Member 只写自己的卷，Guest 只读。

## 构建与测试

```sh
make build       # go build ./...
make lint        # gofmt + go vet
make test        # 单元与验收用例，需要 envtest（自动下载并校验 sha512）
make acceptance  # 只跑 tests/acceptance，一条命令
```

验收用例放 `tests/acceptance/<TC 编号>/`，与设计仓 `kos-storage/03-acceptance.md` 同名：

| 目录 | 判据 |
|---|---|
| `STORAGE-TC-1` | 存储页逐项读取；未接入指标时使用率不可测量；无权限成员看不到删除与迁移入口 |
| `STORAGE-TC-64` | 删除卷的空、错误与正确 purge 令牌；释放的 PVC 出现在残留清单 |
| `STORAGE-TC-65` | 删除应用保留卷与 PVC，PVC 不带释放标记 |
| `STORAGE-TC-66` | 成功迁移六阶段完成，绑定卷指向新卷，应用对象不变，源 PVC 保留 |
| `STORAGE-TC-67` | 五个阶段各注入一次失败都回滚到源卷 |
| `STORAGE-TC-68` | 清理不删除源 PVC |
| `STORAGE-TC-169` | 只读项带来源与原因；页面没有备份、恢复、演练与快照入口 |
| `STORAGE-TC-170` | 迁移入口只在卷已绑定且期望策略与实际策略不一致时出现 |
| `STORAGE-TC-171` | 迁移阶段、失败原因与回滚结果可读，页面没有触发回滚的入口 |

## 依赖

- `kosd`：控制面 API、扩展声明与页面框架、卷/迁移/备份对象与验收测试宿主（`github.com/tsic404/kosd`）。
