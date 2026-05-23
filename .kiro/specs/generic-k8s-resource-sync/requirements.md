# 需求文档：通用 Kubernetes 资源同步

## 简介

本功能将现有的 ConfigMap 专用同步系统升级为通用 Kubernetes 资源同步系统。升级后系统能够在 GitLab 仓库与 Kubernetes 集群之间同步任意类型的 Kubernetes 资源（Deployment、CronJob、Service、Secret、ConfigMap、DaemonSet、StatefulSet、Ingress 等），支持正向同步（GitLab→K8s）和反向同步（K8s→GitLab）两个方向，保留现有的手动、定时、自动三种同步模式。

## 术语表

- **Sync_System**：通用 Kubernetes 资源同步系统，负责在 GitLab 仓库与 Kubernetes 集群之间双向同步资源
- **Dynamic_Client**：基于 `k8s.io/client-go/dynamic` 的通用 Kubernetes 客户端，使用 Unstructured 对象操作任意资源类型
- **GVR**：GroupVersionResource，Kubernetes 资源的唯一标识符，由 API Group、Version、Resource 三部分组成（例如 `apps/v1/deployments`）
- **Unstructured_Object**：`k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.Unstructured` 类型，用于表示任意 Kubernetes 资源的通用数据结构
- **Runtime_Fields**：Kubernetes 运行时自动生成的元数据字段，包括 `status`、`metadata.managedFields`、`metadata.resourceVersion`、`metadata.uid`、`metadata.creationTimestamp`、`metadata.generation`、`metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]`
- **Server_Side_Apply**：Kubernetes 服务端应用机制，等价于 `kubectl apply --server-side`，由服务端负责合并和冲突检测
- **Resource_Type_Selector**：用户在创建同步任务时选择的资源类型集合，定义该任务需要同步哪些 Kubernetes 资源类型
- **YAML_Parser**：通用 YAML 解析器，能够解析任意合法的 Kubernetes 资源 YAML 文件并提取 GVR 信息
- **Resource_Cleaner**：运行时字段清理器，负责在导出资源时移除 Runtime_Fields，生成干净的声明式 YAML
- **Forward_Sync**：正向同步，从 GitLab 仓库读取 YAML 文件并应用到 Kubernetes 集群
- **Reverse_Sync**：反向同步，从 Kubernetes 集群导出资源并提交到 GitLab 仓库
- **Resource_Path_Convention**：GitLab 仓库中资源文件的组织约定，格式为 `{namespace}/{resource_type}/{resource_name}.yaml`

## 需求

### 需求 1：通用 YAML 解析器

**用户故事：** 作为系统开发者，我希望解析器能够处理任意 Kubernetes 资源 YAML 文件，以便系统不再局限于 ConfigMap 类型。

#### 验收标准

1. WHEN 一个合法的 Kubernetes 资源 YAML 文件被提供，THE YAML_Parser SHALL 解析该文件并返回包含 apiVersion、kind、metadata 和 spec/data 字段的 Unstructured_Object
2. WHEN 一个 YAML 文件包含 `apiVersion` 和 `kind` 字段，THE YAML_Parser SHALL 从这两个字段推导出对应的 GVR 信息
3. WHEN 一个 YAML 文件缺少 `apiVersion` 或 `kind` 字段，THE YAML_Parser SHALL 返回包含缺失字段名称的验证错误
4. WHEN 一个 YAML 文件缺少 `metadata.name` 字段，THE YAML_Parser SHALL 返回包含 "metadata.name is required" 的验证错误
5. WHEN 一个包含多个文档的 YAML 文件（以 `---` 分隔）被提供，THE YAML_Parser SHALL 解析所有文档并返回 Unstructured_Object 列表
6. THE YAML_Parser SHALL 将解析后的 Unstructured_Object 格式化回等价的 YAML 文本（Pretty Print）
7. FOR ALL 合法的 Kubernetes 资源 YAML 文件，解析后再格式化再解析 SHALL 产生与首次解析等价的 Unstructured_Object（往返一致性）

### 需求 2：GVR 映射与资源发现

**用户故事：** 作为系统开发者，我希望系统能够将 apiVersion/kind 映射到正确的 GVR，以便 Dynamic_Client 能够操作任意资源类型。

#### 验收标准

1. THE Sync_System SHALL 维护一个内置的 apiVersion/kind 到 GVR 的映射表，覆盖以下常用资源类型：ConfigMap、Secret、Deployment、StatefulSet、DaemonSet、CronJob、Job、Service、Ingress、ServiceAccount、Role、RoleBinding、ClusterRole、ClusterRoleBinding、PersistentVolumeClaim、NetworkPolicy、HorizontalPodAutoscaler
2. WHEN 内置映射表中不存在某个 apiVersion/kind 组合时，THE Sync_System SHALL 通过 Kubernetes Discovery API 动态查询该资源的 GVR
3. WHEN Discovery API 查询失败或返回空结果时，THE Sync_System SHALL 返回包含 apiVersion 和 kind 信息的 "unsupported resource type" 错误
4. THE Sync_System SHALL 区分 Namespaced 资源和 Cluster-scoped 资源，对 Cluster-scoped 资源不传递 namespace 参数
5. THE Sync_System SHALL 缓存 Discovery API 的查询结果，缓存有效期为 5 分钟

### 需求 3：通用 Kubernetes Dynamic Client

**用户故事：** 作为系统开发者，我希望使用 Dynamic Client 替代类型化客户端，以便用统一的接口操作任意 Kubernetes 资源。

#### 验收标准

1. THE Dynamic_Client SHALL 提供 `Apply(ctx, namespace, gvr, obj)` 方法，使用 Server_Side_Apply 将 Unstructured_Object 应用到集群
2. THE Dynamic_Client SHALL 提供 `Get(ctx, namespace, gvr, name)` 方法，从集群获取指定资源并返回 Unstructured_Object
3. THE Dynamic_Client SHALL 提供 `List(ctx, namespace, gvr, labelSelector)` 方法，列出指定命名空间和类型的所有资源
4. THE Dynamic_Client SHALL 提供 `Delete(ctx, namespace, gvr, name)` 方法，从集群删除指定资源
5. THE Dynamic_Client SHALL 提供 `Watch(ctx, namespace, gvr)` 方法，监听指定命名空间和类型的资源变更事件
6. WHEN Server_Side_Apply 操作遇到字段冲突时，THE Dynamic_Client SHALL 使用 `Force: true` 选项强制覆盖冲突字段
7. THE Dynamic_Client SHALL 对所有操作实现指数退避重试机制，最大重试次数为 3 次，基础延迟为 1 秒，最大延迟为 30 秒
8. IF 操作返回 401 Unauthorized、403 Forbidden 或 404 NotFound 错误，THEN THE Dynamic_Client SHALL 不进行重试并立即返回错误

### 需求 4：资源类型选择器

**用户故事：** 作为运维人员，我希望在创建同步任务时选择需要同步的资源类型，以便精确控制同步范围。

#### 验收标准

1. WHEN 用户创建同步任务时，THE Sync_System SHALL 提供资源类型多选界面，允许用户选择一个或多个资源类型
2. THE Sync_System SHALL 提供 "全部资源" 选项，选择后同步目标命名空间中的所有资源类型
3. THE Sync_System SHALL 在同步任务数据结构中新增 `resourceTypes` 字段，存储用户选择的资源类型列表（如 `["ConfigMap", "Secret", "Deployment"]`）
4. WHEN `resourceTypes` 字段为空或包含 "All" 时，THE Sync_System SHALL 同步目标命名空间中所有可发现的资源类型
5. WHEN 用户选择的资源类型在目标集群中不存在（如集群未安装对应 CRD）时，THE Sync_System SHALL 跳过该类型并在同步结果中记录警告信息
6. THE Sync_System SHALL 在任务编辑界面允许用户修改已选择的资源类型

### 需求 5：通用正向同步（GitLab→K8s）

**用户故事：** 作为运维人员，我希望将 GitLab 仓库中的任意 Kubernetes 资源 YAML 文件同步到集群，以便实现 GitOps 工作流。

#### 验收标准

1. WHEN 正向同步被触发时，THE Sync_System SHALL 从 GitLab 仓库获取所有 YAML 文件，解析为 Unstructured_Object，并通过 Dynamic_Client 应用到目标集群
2. WHEN YAML 文件中的资源类型不在任务配置的 `resourceTypes` 范围内时，THE Sync_System SHALL 跳过该文件
3. WHEN YAML 文件中未指定 namespace 时，THE Sync_System SHALL 使用同步任务配置的目标命名空间作为默认值
4. WHEN 目标集群中已存在同名同类型资源且内容相同时，THE Sync_System SHALL 跳过该资源并标记为 "无变更"
5. THE Sync_System SHALL 在比较资源内容时忽略 Runtime_Fields，仅比较 spec、data、metadata.labels、metadata.annotations 等声明式字段
6. WHEN 正向同步完成时，THE Sync_System SHALL 返回同步结果摘要，包含总数、成功数、跳过数、失败数及各资源的详细状态
7. IF 单个资源应用失败，THEN THE Sync_System SHALL 记录错误并继续处理剩余资源，不中断整体同步流程
8. WHEN 一个 YAML 文件包含多个文档（multi-document）时，THE Sync_System SHALL 逐个解析并应用每个文档中的资源

### 需求 6：通用反向同步（K8s→GitLab）

**用户故事：** 作为运维人员，我希望将集群中的任意资源导出到 GitLab 仓库，以便备份集群状态或初始化 GitOps 仓库。

#### 验收标准

1. WHEN 反向同步被触发时，THE Sync_System SHALL 从目标集群列出所有在 `resourceTypes` 范围内的资源
2. THE Resource_Cleaner SHALL 在导出前移除以下 Runtime_Fields：`status`（整个字段）、`metadata.managedFields`、`metadata.resourceVersion`、`metadata.uid`、`metadata.creationTimestamp`、`metadata.generation`、`metadata.selfLink`、`metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]`
3. THE Resource_Cleaner SHALL 移除值为空的 metadata 子字段（如空的 annotations map、空的 labels map）
4. THE Sync_System SHALL 按照 Resource_Path_Convention 组织导出文件，路径格式为 `{base_path}/{namespace}/{resource_type_plural}/{resource_name}.yaml`
5. WHEN GitLab 仓库中已存在同名文件且内容与清理后的资源 YAML 相同时，THE Sync_System SHALL 跳过该资源
6. WHEN 反向同步完成时，THE Sync_System SHALL 返回同步结果摘要，包含总数、成功数、跳过数、失败数
7. IF 单个资源导出失败，THEN THE Sync_System SHALL 记录错误并继续处理剩余资源
8. THE Sync_System SHALL 在 commit message 中包含资源类型、命名空间和资源名称信息

### 需求 7：通用 Watch 监听

**用户故事：** 作为运维人员，我希望系统能够监听多种资源类型的变更事件，以便在自动模式下实时触发同步。

#### 验收标准

1. WHEN 同步任务配置为 auto 模式且方向为 reverse 时，THE Sync_System SHALL 为每个选定的资源类型和命名空间建立独立的 Watch 连接
2. WHEN Watch 检测到资源变更事件（ADDED、MODIFIED）时，THE Sync_System SHALL 触发该资源的反向同步
3. IF Watch 连接断开，THEN THE Sync_System SHALL 使用指数退避策略自动重连，最大重试间隔为 5 分钟
4. WHEN 同步任务配置为 auto 模式且方向为 forward 时，THE Sync_System SHALL 使用 GitLab Webhook 或定时轮询检测文件变更
5. THE Sync_System SHALL 支持同时监听多个命名空间和多个资源类型的组合

### 需求 8：资源差异比较

**用户故事：** 作为运维人员，我希望查看 GitLab 中的期望状态与集群实际状态之间的差异，以便了解漂移情况。

#### 验收标准

1. THE Sync_System SHALL 在比较两个资源时忽略所有 Runtime_Fields
2. THE Sync_System SHALL 对 metadata.labels 和 metadata.annotations 进行比较时，忽略 Kubernetes 运行时自动添加的标签和注解
3. WHEN 两个资源的 spec/data 字段存在差异时，THE Sync_System SHALL 生成字段级别的差异报告，标明每个差异字段的期望值和实际值
4. THE Sync_System SHALL 支持生成完整的 YAML 文本差异（unified diff 格式），用于在 UI 中展示
5. WHEN 资源类型为 Secret 时，THE Sync_System SHALL 在差异展示中对 data 字段的值进行脱敏处理（显示为 "***"）

### 需求 9：GitLab 文件组织规范

**用户故事：** 作为运维人员，我希望 GitLab 仓库中的资源文件按照清晰的目录结构组织，以便快速定位和管理资源文件。

#### 验收标准

1. THE Sync_System SHALL 按照 `{base_path}/{namespace}/{resource_type_plural}/{resource_name}.yaml` 的路径格式组织资源文件
2. THE Sync_System SHALL 使用资源类型的复数形式作为目录名（如 `deployments`、`configmaps`、`secrets`、`services`、`cronjobs`）
3. WHEN 资源为 Cluster-scoped 类型时，THE Sync_System SHALL 使用 `_cluster/{resource_type_plural}/{resource_name}.yaml` 路径格式
4. WHEN 正向同步读取文件时，THE Sync_System SHALL 同时支持新的分类目录结构和旧的扁平目录结构（`{namespace}/{resource_name}.yaml`），实现向后兼容
5. THE Sync_System SHALL 在资源名称中将不合法的文件名字符替换为下划线

### 需求 10：同步任务数据模型扩展

**用户故事：** 作为系统开发者，我希望扩展同步任务的数据模型以支持资源类型选择，以便存储和传递用户的资源类型配置。

#### 验收标准

1. THE Sync_System SHALL 在 SyncTask 数据结构中新增 `resourceTypes []string` 字段
2. WHEN 通过 API 创建同步任务时，THE Sync_System SHALL 接受 `resourceTypes` 参数并持久化
3. WHEN `resourceTypes` 字段未提供或为空时，THE Sync_System SHALL 默认同步所有资源类型（等价于 `["All"]`）
4. THE Sync_System SHALL 保持与现有同步任务的向后兼容性，已存在的不含 `resourceTypes` 字段的任务视为同步 ConfigMap 类型
5. WHEN 通过 API 更新同步任务时，THE Sync_System SHALL 允许修改 `resourceTypes` 字段

### 需求 11：前端资源类型选择界面

**用户故事：** 作为运维人员，我希望在 Web UI 中直观地选择需要同步的资源类型，以便方便地配置同步任务。

#### 验收标准

1. WHEN 用户创建或编辑同步任务时，THE Sync_System SHALL 在表单中展示资源类型多选组件
2. THE Sync_System SHALL 将常用资源类型分组展示：核心资源（ConfigMap、Secret、Service、ServiceAccount）、工作负载（Deployment、StatefulSet、DaemonSet、CronJob、Job）、网络（Ingress、NetworkPolicy）、存储（PersistentVolumeClaim）、RBAC（Role、RoleBinding、ClusterRole、ClusterRoleBinding）
3. THE Sync_System SHALL 提供 "全选" 快捷操作
4. THE Sync_System SHALL 在任务列表中展示已选择的资源类型标签
5. WHEN 用户未选择任何资源类型时，THE Sync_System SHALL 默认选中 "全部资源"

### 需求 12：运行时字段清理规范

**用户故事：** 作为系统开发者，我希望有明确的运行时字段清理规范，以便导出的 YAML 文件干净且可重新应用。

#### 验收标准

1. THE Resource_Cleaner SHALL 移除顶层 `status` 字段
2. THE Resource_Cleaner SHALL 移除 `metadata.managedFields` 字段
3. THE Resource_Cleaner SHALL 移除 `metadata.resourceVersion` 字段
4. THE Resource_Cleaner SHALL 移除 `metadata.uid` 字段
5. THE Resource_Cleaner SHALL 移除 `metadata.creationTimestamp` 字段
6. THE Resource_Cleaner SHALL 移除 `metadata.generation` 字段
7. THE Resource_Cleaner SHALL 移除 `metadata.selfLink` 字段
8. THE Resource_Cleaner SHALL 移除 `metadata.annotations` 中键为 `kubectl.kubernetes.io/last-applied-configuration` 的条目
9. THE Resource_Cleaner SHALL 移除 `metadata.annotations` 中以 `kubernetes.io/`、`k8s.io/`、`control-plane.alpha.kubernetes.io/` 为前缀的条目
10. WHEN 清理后 `metadata.annotations` 为空 map 时，THE Resource_Cleaner SHALL 移除整个 `metadata.annotations` 字段
11. WHEN 清理后 `metadata.labels` 为空 map 时，THE Resource_Cleaner SHALL 移除整个 `metadata.labels` 字段
12. FOR ALL 合法的 Kubernetes 资源，清理后的 YAML 重新应用到集群 SHALL 不产生语义错误（即清理不会移除必要字段）

### 需求 13：向后兼容性

**用户故事：** 作为现有用户，我希望升级后现有的 ConfigMap 同步任务继续正常工作，以便平滑过渡到新系统。

#### 验收标准

1. WHEN 系统升级后加载不含 `resourceTypes` 字段的旧任务时，THE Sync_System SHALL 将其视为仅同步 ConfigMap 类型的任务
2. THE Sync_System SHALL 保留现有的 API 端点和响应格式，新增字段为可选
3. THE Sync_System SHALL 保留现有的 GitLab 文件路径格式支持（`{namespace}/{name}.yaml`），同时支持新的分类路径格式
4. WHEN 正向同步读取旧格式路径的文件时，THE Sync_System SHALL 通过文件内容的 `kind` 字段判断资源类型
5. THE Sync_System SHALL 保留现有的同步历史记录格式，新增的资源类型信息为可选字段

### 需求 14：同步历史记录扩展

**用户故事：** 作为运维人员，我希望同步历史记录包含资源类型信息，以便追踪不同类型资源的同步状态。

#### 验收标准

1. THE Sync_System SHALL 在同步历史记录中新增 `resourceKind` 字段，记录被同步资源的 Kind
2. THE Sync_System SHALL 在同步历史记录中新增 `resourceGroup` 字段，记录被同步资源的 API Group
3. WHEN 查询同步历史时，THE Sync_System SHALL 支持按 `resourceKind` 字段过滤
4. THE Sync_System SHALL 在历史记录的 YAML diff 中展示完整的资源 YAML（清理 Runtime_Fields 后）
5. WHEN 历史记录中的资源类型为 Secret 时，THE Sync_System SHALL 对 data 字段值进行脱敏后再存储

### 需求 15：错误处理与日志

**用户故事：** 作为运维人员，我希望系统在遇到不支持的资源类型或权限不足时提供清晰的错误信息，以便快速定位问题。

#### 验收标准

1. IF Dynamic_Client 操作返回 403 Forbidden 错误，THEN THE Sync_System SHALL 在同步结果中记录 "权限不足：无法操作 {namespace}/{kind}/{name}" 错误信息
2. IF 目标集群不支持某个资源类型（API 返回 404），THEN THE Sync_System SHALL 在同步结果中记录 "资源类型不支持：{apiVersion}/{kind} 在目标集群中不可用" 警告信息
3. IF YAML 文件解析失败，THEN THE Sync_System SHALL 在同步结果中记录文件路径和具体的解析错误信息
4. THE Sync_System SHALL 对每次同步操作记录结构化日志，包含任务 ID、资源类型、命名空间、资源名称、操作结果
5. IF 同步任务连续失败 3 次，THEN THE Sync_System SHALL 将任务状态设置为 "error" 并记录累计错误信息
