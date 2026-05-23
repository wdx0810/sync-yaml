# 实现计划：多源多目标同步任务

## 概述

基于设计文档的依赖关系，按从底层到上层的顺序逐步实现多源多目标同步架构。先实现无依赖的加密服务（crypto），再实现依赖加密的数据存储层（store），然后实现连接测试器，接着重构任务管理器（engine），再扩展 API 层，最后构建前端页面并集成到 main.go 入口。每个阶段之间设置检查点，确保增量开发的正确性。

## 任务

- [x] 1. 实现加密服务（`internal/crypto/`）
  - [x] 1.1 实现 AES-256-GCM 加密/解密服务
    - 创建 `internal/crypto/crypto.go`
    - 定义 `Service` 接口（`Encrypt(plaintext string) (string, error)` 和 `Decrypt(ciphertext string) (string, error)`）
    - 实现 `NewService(keyPath string) (Service, error)`：如果密钥文件不存在则自动生成 32 字节随机密钥并保存（文件权限 0600）
    - 加密流程：生成 12 字节随机 nonce → AES-256-GCM 加密 → 拼接 nonce + ciphertext → base64 编码
    - 解密流程：base64 解码 → 分离 nonce 和 ciphertext → AES-256-GCM 解密
    - _需求：4.1, 4.2, 4.3, 4.6_

  - [ ]* 1.2 编写加密解密往返一致性属性测试
    - **属性 1：加密解密往返一致性**
    - 对任意非空字符串 s，加密后再解密结果应等于原始字符串 s
    - **验证需求：4.1, 4.2, 4.3**

  - [ ]* 1.3 编写加密服务单元测试
    - 测试空字符串加密、超长字符串、特殊字符（中文、emoji）
    - 测试密钥文件自动生成
    - 测试相同明文产生不同密文（随机 nonce）
    - _需求：4.6_

- [x] 2. 实现数据存储层（`internal/store/`）
  - [x] 2.1 实现 GitLab Source Store
    - 创建 `internal/store/types.go`，定义 `GitLabSource`、`K8sTarget`、`SyncTask` 数据结构
    - 创建 `internal/store/source_store.go`，实现 `SourceStore` 接口（List、Get、Create、Update、Delete）
    - 使用 `sync.RWMutex` 保护并发访问
    - 写操作立即持久化到 `gitlab_sources.json`
    - 创建时对 token 和 webhookSecret 字段调用 `crypto.Service.Encrypt` 加密
    - 读取时对加密字段调用 `crypto.Service.Decrypt` 解密
    - 创建时检查 Name 唯一性，重复则返回错误
    - _需求：1.1, 1.2, 1.3, 1.4, 1.7, 4.1_

  - [x] 2.2 实现 K8s Target Store
    - 创建 `internal/store/target_store.go`，实现 `TargetStore` 接口（List、Get、Create、Update、Delete）
    - 使用 `sync.RWMutex` 保护并发访问
    - 写操作立即持久化到 `k8s_targets.json`
    - 创建时对 kubeconfigContent 字段加密
    - 创建时检查 Name 唯一性，重复则返回错误
    - _需求：2.1, 2.2, 2.3, 2.4, 2.7, 4.2_

  - [x] 2.3 实现 Sync Task Store
    - 创建 `internal/store/task_store.go`，实现 `TaskStore` 接口（List、Get、Create、Update、Delete）
    - 使用 `sync.RWMutex` 保护并发访问
    - 写操作立即持久化到 `sync_tasks.json`
    - 创建时自动生成 UUID 作为 ID
    - 创建时验证 sourceName 和 targetName 引用的实体存在（需要 SourceStore 和 TargetStore 引用）
    - 创建时设置初始 Status 为 "paused"
    - _需求：5.1, 5.2, 5.8_

  - [x] 2.4 实现引用完整性保护
    - 在 SourceStore.Delete 和 TargetStore.Delete 中检查是否被 SyncTask 引用
    - 如果被引用则返回错误，包含引用该实体的任务信息
    - _需求：1.5, 2.5_

  - [x] 2.5 实现损坏文件容错加载
    - 加载 JSON 文件时，如果文件不存在或内容损坏（非法 JSON），记录警告日志并使用空配置列表
    - 不 panic、不返回致命错误
    - _需求：8.3, 8.4_

  - [ ]* 2.6 编写 GitLab Source 持久化往返属性测试
    - **属性 2：GitLab Source 持久化往返一致性**
    - 对任意有效的 GitLabSource 配置，创建后通过 Get 读取应返回与原始数据等价的配置（敏感字段解密后一致）
    - **验证需求：1.2, 8.3**

  - [ ]* 2.7 编写 K8s Target 持久化往返属性测试
    - **属性 3：K8s Target 持久化往返一致性**
    - 对任意有效的 K8sTarget 配置，创建后通过 Get 读取应返回与原始数据等价的配置（敏感字段解密后一致）
    - **验证需求：2.2, 8.3**

  - [ ]* 2.8 编写名称唯一性约束属性测试
    - **属性 4：名称唯一性约束**
    - 对任意已存在的 GitLabSource 或 K8sTarget 名称，尝试创建同名实体应返回错误，且原有数据不被修改
    - **验证需求：1.7, 2.7**

  - [ ]* 2.9 编写引用完整性保护属性测试
    - **属性 5：引用完整性保护**
    - 对任意被 SyncTask 引用的 GitLabSource 或 K8sTarget，尝试删除应返回错误并包含引用任务信息，且实体不被删除
    - **验证需求：1.5, 2.5**

  - [ ]* 2.10 编写删除后不可查属性测试
    - **属性 6：删除后不可查**
    - 对任意已创建的 GitLabSource、K8sTarget 或 SyncTask，删除后通过 Get 查询应返回 not found 错误，且 List 结果中不包含该实体
    - **验证需求：1.4, 2.4, 5.4**

  - [ ]* 2.11 编写同步任务创建初始状态属性测试
    - **属性 7：同步任务创建初始状态**
    - 对任意有效的 SyncTask 创建请求（引用存在的 source 和 target），创建后的任务 Status 应为 "paused"
    - **验证需求：5.2**

  - [ ]* 2.12 编写无效引用拒绝创建属性测试
    - **属性 8：无效引用拒绝创建**
    - 对任意 SyncTask 创建请求，如果 sourceName 或 targetName 引用不存在的实体，创建应返回错误且任务不被持久化
    - **验证需求：5.8**

  - [ ]* 2.13 编写损坏文件容错属性测试
    - **属性 14：损坏文件容错**
    - 对任意非法 JSON 内容写入存储文件，Store 加载时应不 panic、不返回致命错误，而是返回空配置列表
    - **验证需求：8.4**

  - [ ]* 2.14 编写 Store 单元测试
    - 测试具体的 CRUD 流程（创建、读取、更新、删除）
    - 测试并发读写安全性
    - 测试 JSON 文件持久化和重新加载
    - _需求：8.3, 8.5_

- [x] 3. 检查点 — 确保加密服务和数据存储层测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 4. 实现连接测试器
  - [x] 4.1 实现 ConnectionTester
    - 创建 `internal/store/connection_tester.go`，定义 `ConnectionTester` 接口
    - 实现 `TestGitLab(url, token string, projectID int) error`：调用 `GET /api/v4/projects/:id` 验证 Token 和项目访问权限
    - 实现 `TestK8s(kubeconfigContent string) error`：使用 kubeconfig 创建客户端，调用 `ListNamespaces` 验证连通性
    - GitLab 测试使用 `go-gitlab` 库，K8s 测试使用 `client-go` 库
    - _需求：3.1, 3.2, 3.3, 3.4_

  - [ ]* 4.2 编写连接测试器单元测试
    - 使用 Mock HTTP Server 模拟 GitLab API 的成功和失败场景
    - 使用 fake clientset 模拟 K8s 的成功和失败场景
    - 测试认证失败、网络超时、连接拒绝等错误场景
    - _需求：3.1, 3.2, 3.3, 3.4_

- [x] 5. 重构任务管理器（`internal/engine/`）
  - [x] 5.1 实现 TaskManager 接口
    - 创建 `internal/engine/task_manager.go`
    - 定义 `TaskManager` 接口（StartTask、StopTask、TriggerSync、GetTaskStatus、StopAll、RestoreRunningTasks）
    - 定义 `TaskRuntimeStatus` 数据结构
    - 每个任务在独立 goroutine 中运行，拥有独立的 `context.Context`
    - 每个任务持有独立的 GitLab Client 和 K8s Client 实例（通过 sourceName/targetName 从 Store 获取配置并创建）
    - _需求：6.1, 6.2_

  - [x] 5.2 实现任务同步模式调度
    - 实现 scheduled 模式：按 interval 周期性执行正向同步
    - 实现 manual 模式：仅在 TriggerSync 调用时执行
    - 实现 auto 模式：监听 webhook 事件触发同步（复用现有 Scheduler 逻辑）
    - 任务失败时设置状态为 "error"，记录错误信息，不影响其他任务
    - _需求：5.5, 5.6, 5.7, 5.9, 5.10, 6.2_

  - [x] 5.3 实现任务恢复与优雅关闭
    - 实现 `RestoreRunningTasks`：服务启动时加载所有 status="running" 的任务并自动恢复执行
    - 实现 `StopAll`：优雅关闭时停止所有运行中的任务，持久化当前状态
    - _需求：6.3, 6.4_

  - [ ]* 5.4 编写任务状态转换正确性属性测试
    - **属性 9：任务状态转换正确性**
    - 对任意状态为 "paused" 的 SyncTask，执行 start 后状态应变为 "running"；对任意状态为 "running" 的 SyncTask，执行 pause 后状态应变为 "paused"
    - **验证需求：5.5, 5.6**

  - [ ]* 5.5 编写任务错误隔离属性测试
    - **属性 10：任务错误隔离**
    - 对任意包含多个运行中 SyncTask 的系统，当其中一个任务同步失败时，该任务状态变为 "error"，而其他任务状态保持不变
    - **验证需求：6.1, 6.2**

  - [ ]* 5.6 编写任务恢复正确性属性测试
    - **属性 11：任务恢复正确性**
    - 对任意持久化的 SyncTask 集合（包含 running、paused、error 状态的混合），服务重启后仅状态为 "running" 的任务被自动恢复执行
    - **验证需求：6.3**

  - [ ]* 5.7 编写 TaskManager 单元测试
    - 测试任务启动和停止流程
    - 测试 scheduled 模式定时触发
    - 测试手动触发同步
    - 测试优雅关闭时任务状态持久化
    - _需求：5.5, 5.6, 5.7, 6.3, 6.4_

- [x] 6. 检查点 — 确保连接测试器和任务管理器测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 7. 扩展 API 层（`internal/api/`）
  - [x] 7.1 实现 Sources API 端点
    - 在 `internal/api/` 中新增 `sources.go`
    - 实现 `GET /api/v1/sources`：列出所有 GitLab 数据源，token 字段返回 "***"，webhookSecret 字段返回 "***"
    - 实现 `POST /api/v1/sources`：创建 GitLab 数据源，名称重复返回 409
    - 实现 `PUT /api/v1/sources/{name}`：更新 GitLab 数据源，不存在返回 404
    - 实现 `DELETE /api/v1/sources/{name}`：删除 GitLab 数据源，被引用返回 422，不存在返回 404
    - 实现 `POST /api/v1/sources/{name}/test`：测试 GitLab 连接
    - _需求：9.1, 4.4_

  - [x] 7.2 实现 Targets API 端点
    - 在 `internal/api/` 中新增 `targets.go`
    - 实现 `GET /api/v1/targets`：列出所有 K8s 目标，kubeconfigContent 字段返回 "已配置"
    - 实现 `POST /api/v1/targets`：创建 K8s 目标，名称重复返回 409
    - 实现 `PUT /api/v1/targets/{name}`：更新 K8s 目标，不存在返回 404
    - 实现 `DELETE /api/v1/targets/{name}`：删除 K8s 目标，被引用返回 422，不存在返回 404
    - 实现 `POST /api/v1/targets/{name}/test`：测试 K8s 连接
    - _需求：9.2, 4.5_

  - [x] 7.3 实现 Tasks API 端点
    - 在 `internal/api/` 中新增 `tasks.go`
    - 实现 `GET /api/v1/tasks`：列出所有同步任务
    - 实现 `POST /api/v1/tasks`：创建同步任务，引用无效返回 400，interval < 30 返回 400
    - 实现 `PUT /api/v1/tasks/{id}`：更新同步任务，不存在返回 404
    - 实现 `DELETE /api/v1/tasks/{id}`：删除同步任务（如果运行中则先停止），不存在返回 404
    - 实现 `POST /api/v1/tasks/{id}/start`：启动同步任务
    - 实现 `POST /api/v1/tasks/{id}/pause`：暂停同步任务
    - 实现 `POST /api/v1/tasks/{id}/sync`：手动触发同步
    - _需求：9.3, 5.3, 5.4, 5.5, 5.6, 5.7_

  - [x] 7.4 实现 Dashboard API 端点
    - 在 `internal/api/` 中新增 `dashboard.go`
    - 实现 `GET /api/v1/dashboard`：返回任务统计摘要（total、running、paused、error）和任务列表
    - 统计数据从 TaskStore 实时计算
    - _需求：9.4, 7.1, 7.2_

  - [x] 7.5 更新 API Server 结构
    - 修改 `internal/api/api.go` 中的 `Server` 结构体，添加 `SourceStore`、`TargetStore`、`TaskStore`、`TaskManager`、`ConnectionTester` 依赖
    - 修改 `NewServer` 函数签名，接受新的依赖参数
    - 在 `registerRoutes` 中注册所有新路由
    - _需求：9.1, 9.2, 9.3, 9.4_

  - [ ]* 7.6 编写仪表盘统计准确性属性测试
    - **属性 12：仪表盘统计准确性**
    - 对任意 SyncTask 集合，Dashboard 返回的 summary 中 total 应等于任务总数，running/paused/error 计数应分别等于对应状态的任务数量，且三者之和等于 total
    - **验证需求：7.1**

  - [ ]* 7.7 编写 API 敏感数据脱敏属性测试
    - **属性 13：API 敏感数据脱敏**
    - 对任意包含敏感字段的 GitLabSource 或 K8sTarget，通过 List/Get API 返回时，token 字段应为 "***"，kubeconfigContent 字段应为 "已配置"
    - **验证需求：4.4, 4.5**

  - [ ]* 7.8 编写 API 错误响应格式属性测试
    - **属性 15：API 错误响应格式**
    - 对任意引用不存在资源的 API 请求，系统应返回 HTTP 404 状态码和包含 "error"、"message"、"status" 字段的 JSON 响应体
    - **验证需求：9.5, 9.6**

  - [ ]* 7.9 编写 API 端点单元测试
    - 测试 Sources CRUD 的具体请求/响应示例
    - 测试 Targets CRUD 的具体请求/响应示例
    - 测试 Tasks CRUD 和状态操作的具体请求/响应示例
    - 测试 Dashboard 空任务列表、全部同一状态等边界情况
    - _需求：9.1, 9.2, 9.3, 9.4, 9.5, 9.6_

- [x] 8. 检查点 — 确保 API 层所有测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 9. 实现前端页面
  - [x] 9.1 扩展 API 客户端（`web/src/api/client.ts`）
    - 新增 TypeScript 类型定义：`GitLabSource`、`K8sTarget`、`SyncTask`、`DashboardData`、`DashboardSummary`
    - 新增 API 方法：`getSources`、`createSource`、`updateSource`、`deleteSource`、`testSource`
    - 新增 API 方法：`getTargets`、`createTarget`、`updateTarget`、`deleteTarget`、`testTarget`
    - 新增 API 方法：`getTasks`、`createTask`、`updateTask`、`deleteTask`、`startTask`、`pauseTask`、`syncTask`
    - 新增 API 方法：`getDashboard`
    - _需求：9.1, 9.2, 9.3, 9.4_

  - [x] 9.2 实现仪表盘页面（`web/src/pages/Dashboard.tsx`）
    - 展示任务统计摘要卡片（总数、运行中、已暂停、错误）
    - 展示任务列表表格（任务名、数据源、目标集群、同步模式、状态、最近同步时间、最近同步结果）
    - 每行提供快捷操作按钮：启动、暂停、手动同步、查看详情
    - 无任务时展示引导信息和创建任务链接
    - 使用 5 秒轮询刷新任务状态
    - _需求：7.1, 7.2, 7.3, 7.4, 7.5_

  - [x] 9.3 实现数据源管理页面（`web/src/pages/Sources.tsx`）
    - 展示 GitLab 数据源列表（名称、URL、项目 ID、连接状态）
    - 实现创建数据源表单弹窗（name、URL、token、projectID、branch、path、webhookSecret）
    - 实现编辑数据源表单弹窗
    - 实现删除确认弹窗
    - 实现连接测试按钮（带 loading 状态）
    - _需求：1.1, 1.2, 1.3, 1.4, 1.6, 1.7, 3.1, 3.3, 3.4, 10.1_

  - [x] 9.4 实现集群目标管理页面（`web/src/pages/Targets.tsx`）
    - 展示 K8s 目标列表（名称、命名空间、连接状态）
    - 实现创建目标表单弹窗（name、kubeconfigContent 文本域、namespace）
    - 实现编辑目标表单弹窗
    - 实现删除确认弹窗
    - 实现连接测试按钮（带 loading 状态）
    - _需求：2.1, 2.2, 2.3, 2.4, 2.6, 2.7, 3.2, 3.3, 3.4, 10.1_

  - [x] 9.5 实现同步任务管理页面（`web/src/pages/Tasks.tsx`）
    - 展示同步任务列表（名称、数据源、目标、同步模式、状态、最近同步时间、结果）
    - 实现创建任务表单弹窗（name、sourceName 下拉选择、targetName 下拉选择、syncMode 选择、interval 输入）
    - 实现编辑任务表单弹窗
    - 实现删除确认弹窗
    - 实现启动、暂停、手动同步操作按钮
    - _需求：5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7, 10.1_

  - [x] 9.6 更新导航和路由（`web/src/App.tsx`）
    - 在侧边导航中添加新菜单项：仪表盘（DashboardOutlined）、数据源管理（CloudServerOutlined）、集群目标（ClusterOutlined）、同步任务（SyncOutlined）
    - 根路径 "/" 重定向到 "/dashboard"
    - 添加新路由：`/dashboard`、`/sources`、`/targets`、`/tasks`
    - 保留现有路由：`/configmaps`、`/drift-alerts`、`/history`、`/settings`
    - _需求：10.1, 10.2, 10.3, 10.4_

  - [ ]* 9.7 编写前端组件单元测试
    - 测试 Dashboard 统计摘要展示和任务列表渲染
    - 测试 Sources 页面的 CRUD 表单和连接测试
    - 测试 Targets 页面的 CRUD 表单和连接测试
    - 测试 Tasks 页面的 CRUD 表单和状态操作
    - 测试导航路由正确性和根路径重定向
    - _需求：7.1 ~ 7.5, 10.1 ~ 10.4_

- [x] 10. 检查点 — 确保前端页面测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 11. 集成到 main.go 入口（`cmd/server/main.go`）
  - [x] 11.1 初始化新组件并集成
    - 初始化 `crypto.Service`（密钥路径 `~/.configmap-sync/encryption.key`）
    - 初始化 `store.SourceStore`、`store.TargetStore`、`store.TaskStore`（存储路径 `~/.configmap-sync/`）
    - 初始化 `store.ConnectionTester`
    - 初始化 `engine.TaskManager`（传入 SourceStore、TargetStore、TaskStore）
    - 修改 `api.NewServer` 调用，传入新的依赖
    - 服务启动时调用 `TaskManager.RestoreRunningTasks` 恢复之前运行中的任务
    - 优雅关闭时调用 `TaskManager.StopAll` 停止所有任务
    - _需求：6.3, 6.4, 8.1, 8.2, 8.3_

  - [ ]* 11.2 编写集成测试
    - 测试服务启动时组件初始化顺序
    - 测试优雅关闭时任务状态持久化
    - 测试密钥文件不存在时自动生成
    - _需求：6.3, 6.4, 8.1_

- [x] 12. 最终检查点 — 确保所有测试通过
  - 确保前后端所有测试通过，如有问题请向用户确认。

## 备注

- 标记 `*` 的子任务为可选任务，可跳过以加速 MVP 交付
- 每个任务引用了具体的需求编号以确保可追溯性
- 检查点任务用于阶段性验证，确保增量开发的正确性
- 属性测试验证通用正确性属性（对应设计文档中的 15 个属性），单元测试验证具体示例和边界情况
- 后端使用 Go 语言，前端使用 TypeScript + React + Ant Design
- 属性测试使用 Go 的 `testing/quick` 包或 `github.com/leanovate/gopter` 库
- 外部依赖（GitLab API、Kubernetes API）通过接口抽象，测试中使用 mock 实现
