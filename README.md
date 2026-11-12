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
| `pkg/storage` | 存储页声明与读数：设备事实、存储类与 Provider（`STORAGE-C-82`、`KOSD-C-47/48`）、卷与阶段（`KOSD-C-38`）、使用率（指标源未接入时不可测量）、备份状态与最近演练（`KOSD-C-41`）、删除入口（`KOSD-C-39`）、迁移入口与阶段/回滚结果（`STORAGE-C-81`、`KOSD-C-40`）、扩展清单与打包（`KOSD-C-18`、`SDK-C-3/4`） |
| `cmd/kos-storage-package` | 打包与装载产物的生成命令 |

页面按 `KOSD-C-53` 标注每项来源类型与来源；只读项给出只读原因。删除与迁移入口按会话角色与所有权裁剪：Owner 可写全部，Member 只写自己的卷，Guest 只读。

## 打包与装载

本产品是独立扩展制品：清单按 `KOSD-C-18` 声明，只贡献存储页（`STORAGE-C-1`、`STORAGE-C-81`）与它的两个入口，
无对象类、控制器、能力、设置项与窗口应用。权限只给只读范围（内核存储对象与存储类）与所需控制面角色，
不含写入范围；依赖只写框架版本；组件配置结构为空、状态接口为组件条件集；卸载保留语义为删除页面、无可保留数据与外部资源。

打包走 kos-sdk 的唯一路径（`SDK-C-4`），第一方与第三方无差异：

```sh
make package                                              # 等价于下一行
go run ./cmd/kos-storage-package -out dist                # 产出包与摘要
go run ./cmd/kos-storage-package -out dist -install-doc dist/kos-storage.json  # 另出装载产物
```

包结构固定为两个条目，本产品没有 UI 资产，所以摘要只覆盖清单：

| 条目 | 内容 |
|---|---|
| `manifest.json` | 校验后的扩展清单 |
| `digest` | 包摘要 `sha256:<hex>`，覆盖清单与其全部 UI 资产 |

同一输入产出同一结构：两次打包的包与摘要一致；清单或任一资产变化都会改变摘要。读取受预算约束：
条目数与内容总字节数各有上限，超限的包判为不可读并指出超限项，不返回部分内容。

装载与卸载都走系统组件这一条通道（`KOSD-C-17`、`KOSD-C-28`、`KOSD-C-29`）：

```sh
# 1. 产出包与内核装载产物（装载产物即系统组件工件：核心安装路径读取声明文档）
go run ./cmd/kos-storage-package -out dist -install-doc dist/kos-storage.json

# 2. 创建系统组件：摘要取命令打印的 install digest
kubectl apply -f - <<'EOF'
apiVersion: kos.tsic.top/v1beta1
kind: SystemComponent
metadata:
  name: storage
  labels:
    kos.tsic.top/managed-by: kosd
spec:
  package:
    reference: dist/kos-storage.json
    digest: sha256:<install digest>
EOF

# 3. 卸载：删除组件对象；页面与入口随之下线，无残留
kubectl delete systemcomponent storage
```

摘要不匹配时组件拒绝装载，声明的页面与入口都不出现；其余产品、内核对象与写者表不受影响。

## 构建与测试

```sh
make build       # go build ./...
make lint        # gofmt + go vet
make test        # 单元与验收用例，需要 envtest（自动下载并校验 sha512）
make acceptance  # 只跑 tests/acceptance，一条命令
make package     # 产出扩展包与摘要（dist/）
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
| `KOSD-TC-155` | 用本仓打包产物以系统组件装载与卸载；页面与两个入口随组件出现与消失，其余产品、内核对象与写者表不变 |

## 依赖

- `kosd`：控制面 API、扩展声明与页面框架、卷/迁移/备份对象与验收测试宿主（`github.com/tsic404/kosd`）。
- `kos-sdk`：扩展清单结构与唯一打包路径（`github.com/tsic404/kos-sdk`）。
