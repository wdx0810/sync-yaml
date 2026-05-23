# 实现计划：通用 Kubernetes 资源同步

## 概述

按模块依赖关系从底层到上层逐步实现。先实现无依赖的核心组件（GVR 解析器、通用解析器、资源清理器、路径提供器），再实现 Dynamic Client，然后重构同步引擎支持通用资源，接着扩展 API 和前端，最后集成测试。

## 任务

- [x] 1. 实现 GVR 解析器（`internal/k8s/gvr/`）
  - [x] 1.1 实现 GVR Resolver
    - 创建 `internal/k8s/gvr/resolver.go`
    - 定义 `Resolver` 接口（Resolve、IsNamespaced、RefreshCache）
    - 实现内置 GVR 映射表（覆盖 17 种常用资源类型）
    - 实现 Discovery API 回退查询（缓存 5 分钟）
    - 区分 Namespaced 和 Cluster-scoped 资源
    - _需求：2.1, 2.2, 2.3, 2.4, 2.5_

  - [ ]* 1.2 编写 GVR 解析确定性属性测试
    - **属性 2**：相同 apiVersion/kind 输入始终返回相同 GVR
    - **验证需求：2.1, 2.2**

- [x] 2. 实现通用 YAML 解析器（`internal/parser/generic/`）
  - [x] 2.1 实现 GenericParser
    - 创建 `internal/parser/generic/parser.go`
    - 定义 `Resource` 结构体和 `Parser` 接口
    - 实现 `Parse`：解析单个 YAML 文档为 Unstructured 对象，提取 apiVersion/kind/name/namespace
    - 实现 `ParseMulti`：支持多文档 YAML（`---` 分隔）
    - 实现 `Print`：将 Unstructured 对象格式化为 YAML
    - 验证必填字段（apiVersion、kind、metadata.name）
    - 调用 GVR Resolver 获取 GVR 信息
    - _需求：1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7_

  - [ ]* 2.2 编写 YAML 往返一致性属性测试
    - **属性 1**：解析后再格式化再解析应产生等价对象
    - **验证需求：1.7**

- [ ] 3. 实现运行时字段清理器（`internal/k8s/cleaner/`）
  - [x] 3.1 实现 ResourceCleaner
    - 创建 `internal/k8s/cleaner/cleaner.go`
    - 定义 `Cleaner` 接口
    - 实现移除顶层 `status` 字段
    - 实现移除 `metadata.managedFields`、`resourceVersion`、`uid`、`creationTimestamp`、`generation`、`selfLink`
    - 实现移除 `metadata.annotations` 中的运行时键（kubectl.kubernetes.io/last-applied-configuration、kubernetes.io/*、k8s.io/*）
    - 实现清理后空 map 移除（空 annotations、空 labels）
    - _需求：12.1-12.12_

  - [ ]* 3.2 编写清理幂等性属性测试
    - **属性 3**：两次 Clean 结果与一次相同
    - **验证需求：12.1-12.12**

- [x] 4. 实现路径提供器（`internal/path/`）
  - [x] 4.1 实现 PathProvider
    - 创建 `internal/path/provider.go`
    - 定义 `Provider` 接口（ResourcePath、ParsePath）
    - 实现路径生成：`{basePath}/{namespace}/{resourceTypePlural}/{name}.yaml`
    - 实现 Cluster-scoped 路径：`{basePath}/_cluster/{resourceTypePlural}/{name}.yaml`
    - 实现路径解析（从路径还原 namespace/type/name）
    - 实现文件名安全字符替换
    - 支持旧格式路径兼容（`{namespace}/{name}.yaml`）
    - _需求：9.1, 9.2, 9.3, 9.4, 9.5_

  - [ ]* 4.2 编写路径唯一性和往返属性测试
    - **属性 6**：不同资源生成不同路径
    - **属性 7**：生成的路径可正确解析还原
    - **验证需求：9.1, 9.4**

- [x] 5. 实现资源差异比较器（`internal/diff/`）
  - [x] 5.1 实现 ResourceDiffer
    - 创建 `internal/diff/differ.go`
    - 定义 `Differ` 接口和 `DiffResult`、`FieldDiff` 结构体
    - 实现比较时忽略运行时字段
    - 实现字段级差异报告（递归比较 spec/data）
    - 实现完整 YAML 文本差异生成
    - 实现 Secret data 字段脱敏
    - _需求：8.1, 8.2, 8.3, 8.4, 8.5_

  - [ ]* 5.2 编写差异比较属性测试
    - **属性 8**：Compare(A,B) 有差异 ⟺ Compare(B,A) 有差异
    - **验证需求：8.1**

- [x] 6. 检查点 — 确保核心组件编译通过
  - 确保所有核心组件编译通过，如有问题请向用户确认。

- [x] 7. 实现 Dynamic Client（`internal/k8s/dynamic/`）
  - [x] 7.1 实现 DynamicClient
    - 创建 `internal/k8s/dynamic/client.go`
    - 定义 `Client` 接口（Apply、Get、List、Delete、Watch）
    - 实现 Server-Side Apply（PATCH + ApplyPatchType + Force=true）
    - 实现 Get/List/Delete 操作
    - 实现 Watch 操作（返回 event channel）
    - 实现指数退避重试（最多 3 次，401/403/404 不重试）
    - 从 kubeconfig 内容创建 dynamic client（复用现有 kubeconfig 解析逻辑）
    - _需求：3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8_

- [x] 8. 重构同步引擎（`internal/engine/`）
  - [x] 8.1 实现通用正向同步
    - 修改 `doForwardSync`，使用 GenericParser + DynamicClient 替代 ConfigMap 专用逻辑
    - 支持 resourceTypes 过滤
    - 支持多文档 YAML 文件
    - 使用 ResourceDiffer 判断是否有变更
    - 记录详细的同步历史（含 YAML diff）
    - _需求：5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7, 5.8_

  - [x] 8.2 实现通用反向同步
    - 修改 `doReverseSync`，使用 DynamicClient.List + ResourceCleaner + PathProvider
    - 遍历所有选定的 resourceTypes
    - 使用新的文件路径规范
    - 使用 ResourceDiffer 判断是否有变更
    - _需求：6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 6.8_

  - [x] 8.3 实现通用 Watch 模式
    - 修改 `runWatchMode`，为每个 resourceType × namespace 建立 Watch
    - 实现事件防抖（2 秒窗口合并）
    - 实现 Watch 断线重连（指数退避，最大 5 分钟）
    - _需求：7.1, 7.2, 7.3, 7.5_

  - [x] 8.4 实现向后兼容
    - 旧任务（无 resourceTypes 字段）视为仅同步 ConfigMap
    - 保留旧文件路径格式支持
    - _需求：13.1, 13.3, 13.4_

  - [ ]* 8.5 编写资源类型过滤属性测试
    - **属性 5**：过滤后结果仅包含选定类型
    - **验证需求：4.3, 5.2**

  - [ ]* 8.6 编写无变更不提交属性测试
    - **属性 9**：内容相同时不提交 GitLab
    - **验证需求：6.5**

  - [ ]* 8.7 编写向后兼容属性测试
    - **属性 10**：旧任务视为 ConfigMap 类型
    - **验证需求：13.1**

- [x] 9. 检查点 — 确保引擎重构编译通过
  - 确保所有测试通过，如有问题请向用户确认。

- [x] 10. 扩展数据模型和 API
  - [x] 10.1 扩展 SyncTask 数据模型
    - 在 `store.SyncTask` 中新增 `ResourceTypes []string` 字段
    - 更新 TaskStore 的 Create/Update 逻辑
    - 保持向后兼容（空字段 = ConfigMap）
    - _需求：10.1, 10.2, 10.3, 10.4, 10.5_

  - [x] 10.2 扩展同步历史数据模型
    - 在 `history.ChangeDetail` 中新增 `Kind` 和 `Group` 字段
    - 更新历史查询支持按 resourceKind 过滤
    - Secret 类型 data 字段脱敏存储
    - _需求：14.1, 14.2, 14.3, 14.4, 14.5_

  - [x] 10.3 更新 API 端点
    - Tasks API 接受和返回 resourceTypes 字段
    - History API 支持 resourceKind 过滤参数
    - 保留现有 API 格式兼容
    - _需求：10.5, 13.2, 14.3_

- [x] 11. 实现前端资源类型选择
  - [x] 11.1 实现资源类型选择组件
    - 创建 `web/src/components/ResourceTypeSelector.tsx`
    - 分组展示：核心资源、工作负载、网络、存储、RBAC
    - 支持全选/取消全选
    - 支持搜索过滤
    - _需求：11.1, 11.2, 11.3_

  - [x] 11.2 更新任务创建/编辑表单
    - 在创建/编辑同步任务表单中集成 ResourceTypeSelector
    - 任务列表展示已选资源类型标签
    - 默认选中 "全部资源"
    - _需求：11.4, 11.5_

  - [x] 11.3 更新同步历史页面
    - 添加 resourceKind 过滤器
    - 在详情展示中显示资源类型
    - _需求：14.3_

  - [x] 11.4 更新 API 客户端类型定义
    - 扩展 TypeScript 类型：SyncTask 新增 resourceTypes
    - 扩展 HistoryFilter 新增 resourceKind
    - _需求：10.5, 14.3_

- [x] 12. 检查点 — 确保前端编译通过
  - 确保前端构建通过，如有问题请向用户确认。

- [x] 13. 集成与最终验证
  - [x] 13.1 更新 main.go 集成新组件
    - 初始化 GVR Resolver、GenericParser、ResourceCleaner、DynamicClient、PathProvider、ResourceDiffer
    - 传入 TaskManager
    - _需求：全部_

  - [x] 13.2 端到端测试
    - 正向同步：GitLab 多种资源 YAML → K8s
    - 反向同步：K8s 多种资源 → GitLab（新目录结构）
    - Watch 模式：K8s 资源变更 → 自动同步到 GitLab
    - 向后兼容：旧 ConfigMap 任务继续工作

- [x] 14. 最终检查点 — 确保所有测试通过
  - 确保前后端所有测试通过，如有问题请向用户确认。

## 备注

- 标记 `*` 的子任务为可选任务（属性测试），可跳过以加速 MVP 交付
- 核心创新点：Dynamic Client + Server-Side Apply + GVR 智能解析 + 运行时字段清理
- 向后兼容是关键约束：现有 ConfigMap 任务必须无感升级
- 属性测试使用 `pgregory.net/rapid`，每个属性至少 200 次迭代
