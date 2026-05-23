# 实现计划：K8s ConfigMap YAML 同步系统

## 概述

基于模块化架构，按组件依赖关系从底层到上层逐步实现。先搭建项目结构和基础组件（配置管理、YAML 解析），再实现外部客户端（GitLab、K8s），然后构建核心引擎（同步引擎、漂移检测、历史存储），接着实现 HTTP API 层，最后构建 Web 前端并集成静态文件服务。

## 任务

- [x] 1. 搭建项目结构与配置管理
  - [x] 1.1 初始化 Go 模块并创建项目目录结构
    - 创建 `go.mod`，初始化模块路径
    - 创建目录结构：`cmd/server/`、`internal/config/`、`internal/parser/`、`internal/gitlab/`、`internal/k8s/`、`internal/engine/`、`internal/drift/`、`internal/history/`、`internal/webhook/`、`internal/api/`
    - 创建 `cmd/server/main.go` 入口文件骨架
    - _需求：6.1_

  - [x] 1.2 实现 Config Manager（`internal/config/`）
    - 定义 `Config`、`GitLabConfig`、`K8sConfig`、`SyncConfig`、`DriftConfig`、`HistoryConfig`、`APIConfig` 结构体
    - 实现 `LoadConfig(path string) (*Config, error)` 函数，从 YAML 文件加载配置并应用默认值
    - 实现 `Validate() error` 方法，验证必填参数、Sync_Mode 合法性、auto 模式 Webhook Secret、scheduled 模式 Interval >= 30 秒
    - _需求：6.1, 6.2, 6.3, 6.4, 6.5, 6.6_

  - [ ]* 1.3 编写配置验证属性测试
    - **属性 21：配置验证** — 对于任意不合法配置，Validate 应返回包含具体错误描述的错误
    - **验证需求：6.2, 6.4, 6.5, 6.6**

  - [ ]* 1.4 编写配置 YAML 解析属性测试
    - **属性 22：配置 YAML 解析** — 对于任意合法配置 YAML，LoadConfig 应正确解析所有参数
    - **验证需求：6.1**

  - [ ]* 1.5 编写配置管理单元测试
    - 测试默认配置值正确应用（6.3）
    - 测试缺少必填参数时的错误报告
    - _需求：6.3_

- [x] 2. 实现 YAML 解析与格式化（`internal/parser/`）
  - [x] 2.1 实现 YAML Parser 和 Printer
    - 定义 `ConfigMapData`、`Metadata` 数据结构
    - 实现 `Parse(content []byte) (*ConfigMapData, error)` 函数
    - 实现 `Validate(cm *ConfigMapData) error` 函数，验证 apiVersion、kind、metadata.name、data 字段
    - 实现 `Print(cm *ConfigMapData) ([]byte, error)` 函数
    - _需求：2.1, 2.2, 2.3, 2.4, 2.5_

  - [ ]* 2.2 编写 YAML 往返一致性属性测试
    - **属性 5：YAML 往返一致性** — 对于任意合法 ConfigMap 对象，Print 后再 Parse 应产生等价结果
    - **验证需求：2.1, 2.2, 2.5, 2.6**

  - [ ]* 2.3 编写非法 YAML 错误报告属性测试
    - **属性 6：非法 YAML 错误报告** — 对于任意不合法 YAML 输入，Parser 应返回包含错误位置信息的错误
    - **验证需求：2.3, 2.4**

- [x] 3. 检查点 — 确保基础组件测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 4. 实现 GitLab Client（`internal/gitlab/`）
  - [x] 4.1 实现 GitLab Client 接口与核心方法
    - 定义 `Client` 接口（FetchFiles、CheckChanges、CommitFile）
    - 定义 `FileContent`、`FileChange`、`ChangeType` 数据结构
    - 实现基于 `go-gitlab` 库的客户端，支持拉取 `.yaml`/`.yml` 文件、检测变更、回写提交
    - 实现认证失败（HTTP 401/403）和网络超时的错误处理
    - _需求：1.1, 1.2, 1.3, 1.4, 1.8, 1.9_

  - [ ]* 4.2 编写文件扩展名过滤属性测试
    - **属性 1：YAML 文件扩展名过滤** — 拉取结果应仅包含 `.yaml` 或 `.yml` 扩展名的文件
    - **验证需求：1.1**

  - [ ]* 4.3 编写变更检测完整性属性测试
    - **属性 2：变更检测完整性** — 变更检测应正确识别所有新增、修改和删除的文件
    - **验证需求：1.2, 1.3**

  - [ ]* 4.4 编写 GitLab Client 单元测试
    - 测试 GitLab API 回写（1.4）、认证失败处理（1.8）、网络超时处理（1.9）
    - _需求：1.4, 1.8, 1.9_

- [x] 5. 实现 K8s Client（`internal/k8s/`）
  - [x] 5.1 实现 K8s Client 接口与核心方法
    - 定义 `Client` 接口（ApplyConfigMap、GetConfigMap、ListConfigMaps、DeleteConfigMap）
    - 实现基于 `client-go` 库的客户端
    - 实现与 K8s API Server 通信失败时的指数退避重试（最多 3 次）
    - _需求：3.1, 3.5_

- [x] 6. 实现 Webhook Receiver（`internal/webhook/`）
  - [x] 6.1 实现 Webhook Receiver
    - 定义 `Receiver` 接口（Handler、Events）和 `PushEvent` 数据结构
    - 实现 GitLab Push Event 的接收和解析
    - 实现 Secret Token 签名验证，验证失败返回 HTTP 403
    - 实现事件通道，将合法事件通知 Sync_Engine
    - _需求：1.5, 1.6, 1.7_

  - [ ]* 6.2 编写 Webhook 签名验证属性测试
    - **属性 3：Webhook 签名验证** — 签名不匹配时应返回 HTTP 403
    - **验证需求：1.7**

  - [ ]* 6.3 编写 Webhook 事件触发属性测试
    - **属性 4：Webhook 事件触发正向同步** — 合法 Push Event 涉及 YAML 变更时应触发 Forward_Sync
    - **验证需求：1.6**

  - [ ]* 6.4 编写 Webhook Receiver 单元测试
    - 测试 Auto 模式下 Webhook 监听启动（1.5）
    - _需求：1.5_

- [x] 7. 实现 History Store（`internal/history/`）
  - [x] 7.1 实现 History Store
    - 定义 `Store` 接口（Save、Query、Flush）
    - 定义 `SyncRecord`、`QueryFilter` 数据结构
    - 实现 JSON 文件持久化存储
    - 实现按名称、命名空间、方向、时间范围的过滤查询，结果按时间戳降序排列
    - 实现写入失败时的内存缓存和恢复后重写机制
    - _需求：5.1, 5.2, 5.3, 5.4, 5.5_

  - [ ]* 7.2 编写 SyncRecord 字段完整性属性测试
    - **属性 18：SyncRecord 包含所有必填字段** — 生成的 SyncRecord 应包含所有必填字段
    - **验证需求：5.1**

  - [ ]* 7.3 编写 SyncRecord JSON 往返一致性属性测试
    - **属性 19：SyncRecord JSON 往返一致性** — JSON 序列化后再反序列化应产生等价结果
    - **验证需求：5.2**

  - [ ]* 7.4 编写历史查询过滤与排序属性测试
    - **属性 20：历史查询过滤与排序** — 查询结果应仅包含匹配过滤条件的记录，按时间戳降序排列
    - **验证需求：5.3, 5.4**

  - [ ]* 7.5 编写 History Store 单元测试
    - 测试写入失败时的内存缓存机制（5.5）
    - _需求：5.5_

- [x] 8. 检查点 — 确保客户端和存储组件测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 9. 实现 Sync Engine（`internal/engine/`）
  - [x] 9.1 实现 Sync Engine 核心逻辑
    - 定义 `Engine` 接口（ForwardSync、ForwardSyncOne、ReverseSync、CheckGitLabChanges、GetManagedConfigMaps、GetConfigMapDetail）
    - 定义 `ForwardSyncOptions`、`SyncResult`、`ChangeCheckResult`、`ConfigMapStatus`、`ConfigMapDetail`、`DiffEntry` 数据结构
    - 实现正向同步流程：拉取变更 → YAML 验证 → 应用到 K8s → 记录历史
    - 实现单个 ConfigMap 正向同步（ForwardSyncOne）
    - 实现反向同步流程：读取 Actual_State → 回写 GitLab → 记录历史
    - 实现 GitLab 变更检查
    - _需求：3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 4.4_

  - [x] 9.2 实现三种同步模式调度
    - 实现 Auto 模式：监听 Webhook 事件通道，仅同步事件涉及的文件，合并短时间内多次事件
    - 实现 Scheduled 模式：按 Sync_Interval 周期性拉取并检查变更，无变更时跳过
    - 实现 Manual 模式：仅在 API 触发时执行，待同步变更标记为 Pending，提供 Diff 展示
    - _需求：3.7, 3.8, 3.9, 3.10, 3.11, 3.12, 3.13, 3.14, 3.15_

  - [ ]* 9.3 编写指定 ConfigMap 同步范围属性测试
    - **属性 7：指定 ConfigMap 同步范围** — ForwardSyncOne 应仅同步指定的 ConfigMap
    - **验证需求：3.2**

  - [ ]* 9.4 编写同步状态属性测试
    - **属性 8：同步状态正确反映结果** — 成功为 Synced，重试失败为 Failed
    - **验证需求：3.3, 3.5, 3.6**

  - [ ]* 9.5 编写非法文件跳过属性测试
    - **属性 9：非法文件跳过** — YAML 验证失败的文件应被跳过
    - **验证需求：3.4**

  - [ ]* 9.6 编写 Auto 模式同步范围属性测试
    - **属性 10：Auto 模式仅同步 Webhook 指定文件** — 仅同步 Webhook 事件中的文件
    - **验证需求：3.8**

  - [ ]* 9.7 编写 Webhook 事件合并属性测试
    - **属性 11：Webhook 事件合并** — 短时间内多个事件应合并为一次同步
    - **验证需求：3.9**

  - [ ]* 9.8 编写无变更跳过同步属性测试
    - **属性 12：无变更时跳过同步** — 无新变更时不执行 Forward_Sync
    - **验证需求：3.12**

  - [ ]* 9.9 编写手动模式 Pending 标记属性测试
    - **属性 13：手动模式变更标记为 Pending** — Manual 模式下变更应标记为 Pending
    - **验证需求：3.14**

  - [ ]* 9.10 编写手动模式 Diff 展示属性测试
    - **属性 14：手动模式 Diff 展示** — 应生成包含变更字段和前后值的差异信息
    - **验证需求：3.15**

  - [ ]* 9.11 编写 Sync Engine 单元测试
    - 测试正向同步完整流程（3.1）、Auto 模式触发（3.7）、定时同步周期（3.10, 3.11）、手动模式限制（3.13）
    - _需求：3.1, 3.7, 3.10, 3.11, 3.13_

- [x] 10. 实现 Drift Detector（`internal/drift/`）
  - [x] 10.1 实现 Drift Detector
    - 定义 `Detector` 接口（Start、Stop、GetAlerts、DismissAlert、ResolveAlert）
    - 定义 `DriftAlert` 数据结构
    - 实现周期性漂移检测循环：读取 Actual_State → 获取 Desired_State → 比较差异 → 生成告警
    - 实现告警状态管理（Pending → Dismissed / Resolved）
    - _需求：4.1, 4.2, 4.3, 4.5, 4.6, 4.7_

  - [ ]* 10.2 编写漂移检测生成告警属性测试
    - **属性 15：漂移检测生成告警** — Actual_State 与 Desired_State 不一致时应生成 Drift_Alert
    - **验证需求：4.2**

  - [ ]* 10.3 编写 DriftAlert 状态转换属性测试
    - **属性 16：DriftAlert 状态转换** — dismiss 变为 Dismissed，成功 Reverse_Sync 变为 Resolved
    - **验证需求：4.5, 4.6**

  - [ ]* 10.4 编写漂移告警过滤属性测试
    - **属性 17：漂移告警 API 仅返回未处理告警** — 仅返回 Pending 状态的告警
    - **验证需求：4.3**

  - [ ]* 10.5 编写 Drift Detector 单元测试
    - 测试漂移检测周期运行（4.1）、反向同步触发（4.4）、回写失败保留 Pending（4.7）
    - _需求：4.1, 4.4, 4.7_

- [x] 11. 检查点 — 确保核心引擎测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 12. 实现 HTTP API Server（`internal/api/`）
  - [x] 12.1 实现 API 路由和 Handler
    - 使用 `gorilla/mux` 创建路由器
    - 实现 `GET /api/v1/configmaps` — 列出所有 ConfigMap
    - 实现 `GET /api/v1/configmaps/{namespace}/{name}` — 获取 ConfigMap 详情（含 Desired/Actual 对比）
    - 实现 `POST /api/v1/forward-sync` — 触发全量正向同步
    - 实现 `POST /api/v1/forward-sync/{namespace}/{name}` — 触发单个 ConfigMap 正向同步
    - 实现 `POST /api/v1/reverse-sync/{namespace}/{name}` — 触发反向同步
    - 实现 `GET /api/v1/drift-alerts` — 获取漂移告警列表
    - 实现 `POST /api/v1/drift-alerts/{id}/dismiss` — 忽略漂移告警
    - 实现 `GET /api/v1/history` — 查询同步历史（支持 name、namespace、direction、since、until 参数）
    - 实现 `POST /api/v1/check-gitlab` — 触发 GitLab 变更检查
    - 实现错误处理：不存在返回 404，参数不合法返回 400
    - _需求：7.1, 7.2, 7.3, 7.4, 7.5, 7.6, 7.7, 7.8_

  - [ ]* 12.2 编写 API ConfigMap 列表属性测试
    - **属性 23：API ConfigMap 列表完整性** — 返回列表应包含所有 ConfigMap 及必要字段
    - **验证需求：7.2**

  - [ ]* 12.3 编写 API 错误响应属性测试
    - **属性 24：API 错误响应** — 不存在返回 404，参数不合法返回 400
    - **验证需求：7.7, 7.8**

  - [ ]* 12.4 编写 API Handler 单元测试
    - 测试 API 服务启动（7.1）、GitLab 变更检查触发（7.6）
    - _需求：7.1, 7.6_

- [x] 13. 实现静态文件服务（`internal/api/`）
  - [x] 13.1 实现 Static File Server
    - 使用 Go `embed` 包嵌入 `web/dist/` 前端构建产物
    - 实现 `RegisterStaticRoutes`：API 请求交给 API handler，静态资源返回对应文件，其他请求回退到 index.html
    - _需求：8.14, 8.15, 8.16_

  - [ ]* 13.2 编写静态文件服务 SPA 回退属性测试
    - **属性 25：静态文件服务 SPA 回退** — 非 API 且非静态资源请求应返回 index.html
    - **验证需求：8.15, 8.16**

  - [ ]* 13.3 编写静态文件服务单元测试
    - 测试静态文件嵌入服务（8.14）、SPA 路由回退（8.16）
    - _需求：8.14, 8.16_

- [x] 14. 实现应用入口与组件集成（`cmd/server/main.go`）
  - 加载配置文件，初始化所有组件（GitLab Client、K8s Client、Sync Engine、Drift Detector、History Store、Webhook Receiver）
  - 根据 Sync_Mode 启动对应的同步模式调度
  - 启动 Drift Detector 漂移检测循环
  - 启动 HTTP Server（API + 静态文件服务 + Webhook）
  - 实现优雅关闭（graceful shutdown）
  - _需求：6.1, 6.2, 7.1_

- [x] 15. 检查点 — 确保后端所有测试通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 16. 搭建前端项目结构
  - [x] 16.1 初始化 React + TypeScript + Vite 前端项目
    - 在 `web/` 目录下初始化 Vite + React + TypeScript 项目
    - 安装依赖：`antd`、`react-router-dom`、`axios`、`react-diff-viewer-continued`、`react-syntax-highlighter`
    - 配置 `vite.config.ts`，设置 API 代理到 `http://localhost:8080`，构建输出到 `dist/`
    - 创建 `src/main.tsx` 入口和 `src/App.tsx` 根组件（含路由配置）
    - _需求：8.14, 8.15_

  - [x] 16.2 实现 API 客户端（`web/src/api/client.ts`）
    - 使用 Axios 封装所有后端 API 调用
    - 定义 TypeScript 类型：`ConfigMapStatus`、`ConfigMapDetail`、`DriftAlert`、`SyncRecord`、`HistoryFilter`
    - 实现统一错误处理拦截器
    - _需求：8.17, 8.18_

- [x] 17. 实现前端页面组件
  - [x] 17.1 实现通用组件
    - 实现 `SyncStatusBadge` 同步状态标签组件
    - 实现 `ErrorAlert` 错误提示组件（展示错误状态码和描述，提供重试按钮）
    - 实现 `YamlHighlight` YAML 语法高亮组件（基于 react-syntax-highlighter）
    - 实现 `YamlDiffView` YAML 并排 Diff 组件（基于 react-diff-viewer-continued）
    - _需求：8.4, 8.5, 8.17, 8.18_

  - [x] 17.2 实现 ConfigMap 列表页面（`ConfigMapList.tsx`）
    - 展示所有 ConfigMap 列表（名称、命名空间、同步状态、最近同步时间）
    - 实现"全量同步"按钮，调用 `POST /api/v1/forward-sync`
    - 实现"检查 GitLab 变更"按钮，调用 `POST /api/v1/check-gitlab`
    - 实现自动轮询更新同步状态
    - _需求：8.1, 8.2, 8.6, 8.8_

  - [x] 17.3 实现 ConfigMap 详情页面（`ConfigMapDetail.tsx`）
    - 展示 Desired_State 和 Actual_State 的完整 YAML 内容（语法高亮）
    - 使用 YamlDiffView 并排展示差异并高亮变更行
    - 实现"同步此 ConfigMap"按钮，调用 `POST /api/v1/forward-sync/{namespace}/{name}`
    - _需求：8.3, 8.4, 8.5, 8.7_

  - [x] 17.4 实现漂移告警页面（`DriftAlerts.tsx`）
    - 展示未处理的 Drift_Alert 列表（ConfigMap 名称、命名空间、差异字段、检测时间）
    - 实现"反向同步"按钮，调用 `POST /api/v1/reverse-sync/{namespace}/{name}`
    - 实现"忽略"按钮，调用 `POST /api/v1/drift-alerts/{id}/dismiss`
    - _需求：8.9, 8.10, 8.11_

  - [x] 17.5 实现同步历史页面（`SyncHistory.tsx`）
    - 展示同步历史记录列表，按时间戳降序排列
    - 实现按 ConfigMap 名称、命名空间、同步方向和时间范围的过滤查询
    - _需求：8.12, 8.13_

  - [ ]* 17.6 编写前端组件单元测试
    - 测试 ConfigMap 列表展示、全量同步按钮、GitLab 变更检查按钮（8.1, 8.6, 8.8）
    - 测试 YAML 内容展示、Diff 视图、语法高亮、单个同步按钮（8.3, 8.4, 8.5, 8.7）
    - 测试告警列表展示、反向同步按钮、忽略按钮（8.9, 8.10, 8.11）
    - 测试历史记录展示、过滤查询（8.12, 8.13）
    - 测试 API 错误展示、网络错误重试（8.17, 8.18）
    - _需求：8.1 ~ 8.18_

- [x] 18. 最终检查点 — 确保所有测试通过
  - 确保前后端所有测试通过，如有问题请向用户确认。

## 备注

- 标记 `*` 的子任务为可选任务，可跳过以加速 MVP 交付
- 每个任务引用了具体的需求编号以确保可追溯性
- 检查点任务用于阶段性验证，确保增量开发的正确性
- 属性测试验证通用正确性属性，单元测试验证具体示例和边界情况
- 外部依赖（GitLab API、Kubernetes API）通过接口抽象，测试中使用 mock 实现
