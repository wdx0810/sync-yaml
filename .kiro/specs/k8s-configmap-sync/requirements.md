# 需求文档

## 简介

K8s ConfigMap YAML 同步系统是一个轻量级的双向配置同步工具。系统以 GitLab 仓库中的 ConfigMap YAML 文件作为配置的权威来源（Source of Truth），支持将 GitLab 中的 YAML 变更同步到 Kubernetes 集群（正向同步），正向同步提供三种触发模式：自动同步（通过 GitLab Webhook 感知变更后立即同步）、定时同步（按配置的时间间隔周期性拉取并同步）、手动同步（用户通过 API 手动触发）。同时系统持续监测 K8s 集群中 ConfigMap 的实际状态变化，当检测到集群侧被直接修改时主动提示用户，由用户决定是否将变更回写到 GitLab（反向同步）。

## 术语表

- **Sync_Engine**: 同步引擎，系统核心组件，负责协调 GitLab 与 Kubernetes 之间的 ConfigMap 同步流程
- **GitLab_Client**: GitLab 客户端，负责通过 GitLab API 拉取仓库中的 YAML 文件、检测文件变更、以及将变更回写到 GitLab 仓库的组件
- **YAML_Parser**: YAML 解析器，负责解析和验证 ConfigMap YAML 文件的组件
- **YAML_Printer**: YAML 格式化器，负责将 ConfigMap 对象格式化输出为合法 YAML 文本的组件
- **History_Store**: 历史记录存储，负责持久化保存所有 ConfigMap 同步操作记录的组件
- **K8s_Client**: Kubernetes 客户端，负责与 Kubernetes API Server 通信以读取和应用 ConfigMap 的组件
- **Drift_Detector**: 漂移检测器，负责定期比较 GitLab 中的 Desired_State 与 K8s 集群中的 Actual_State，检测双向差异的组件
- **Sync_Record**: 同步记录，包含时间戳、变更内容、同步方向、同步状态等信息的数据结构
- **Desired_State**: 期望状态，GitLab 仓库中 YAML 文件所定义的 ConfigMap 配置
- **Actual_State**: 实际状态，Kubernetes 集群中当前运行的 ConfigMap 配置
- **Forward_Sync**: 正向同步，将 GitLab 仓库中的 YAML 变更应用到 K8s 集群的操作，支持三种触发模式（Auto_Sync、Scheduled_Sync、Manual_Sync）
- **Sync_Mode**: 同步模式，Forward_Sync 的触发方式，取值为 "auto"、"scheduled" 或 "manual"
- **Auto_Sync**: 自动同步模式，当 GitLab 仓库中的 YAML 文件有新提交时，系统通过 Webhook 感知变更并立即触发 Forward_Sync
- **Scheduled_Sync**: 定时同步模式，系统按配置的时间间隔周期性从 GitLab 拉取最新 YAML 文件，检测到变更后自动触发 Forward_Sync
- **Manual_Sync**: 手动同步模式，用户通过 API 手动触发 Forward_Sync
- **Webhook_Receiver**: Webhook 接收器，负责接收 GitLab Push Event Webhook 回调并通知 Sync_Engine 触发 Auto_Sync 的组件
- **Reverse_Sync**: 反向同步，将 K8s 集群中被直接修改的 ConfigMap 回写到 GitLab 仓库的操作
- **Drift**: 漂移，Desired_State 与 Actual_State 之间的差异，包括 GitLab 侧变更导致的正向漂移和 K8s 侧直接修改导致的反向漂移
- **Drift_Alert**: 漂移告警，当检测到 K8s 集群中的 ConfigMap 被直接修改时，系统向用户发出的通知
- **Web_UI**: Web 前端界面，基于 React + TypeScript 构建的单页应用，提供 ConfigMap 管理、YAML 展示/对比、同步操作和漂移告警管理等可视化功能
- **YAML_Diff_View**: YAML 差异视图，并排展示 Desired_State 和 Actual_State 的 YAML 内容，高亮显示变更行的组件
- **Static_File_Server**: 静态文件服务，后端通过 Go embed 包将前端构建产物嵌入二进制文件并提供 HTTP 静态文件服务的组件

## 需求

### 需求 1：GitLab 仓库集成

**用户故事：** 作为运维工程师，我希望系统能连接 GitLab 仓库并拉取 ConfigMap YAML 文件，以便将 GitLab 作为配置的权威来源进行管理。

#### 验收标准

1. WHEN 系统启动时指定了 GitLab 仓库地址、访问令牌和目标分支, THE GitLab_Client SHALL 成功连接到 GitLab 仓库并拉取指定路径下所有 `.yaml` 和 `.yml` 扩展名的文件
2. WHEN 用户触发 GitLab 变更检查时, THE GitLab_Client SHALL 通过 GitLab API 获取目标分支上指定路径下 YAML 文件的最新提交信息，并与本地缓存的版本进行比较
3. WHEN GitLab_Client 检测到文件有新提交时, THE GitLab_Client SHALL 向 Sync_Engine 报告变更文件列表，包含文件路径和变更类型（新增、修改、删除）
4. WHEN GitLab_Client 需要回写变更到 GitLab 时, THE GitLab_Client SHALL 通过 GitLab API 创建一个新的 commit，将变更内容提交到目标分支
5. WHILE Sync_Mode 配置为 "auto" 时, THE Webhook_Receiver SHALL 在配置的端口上监听 GitLab Push Event Webhook 回调
6. WHEN Webhook_Receiver 收到合法的 GitLab Push Event 且涉及目标分支上的 YAML 文件变更时, THE Webhook_Receiver SHALL 通知 Sync_Engine 触发一次 Forward_Sync
7. IF Webhook_Receiver 收到的请求签名验证失败（Secret Token 不匹配）, THEN THE Webhook_Receiver SHALL 返回 HTTP 403 并记录错误日志
8. IF GitLab API 返回认证失败（HTTP 401 或 403）, THEN THE GitLab_Client SHALL 记录错误日志并向用户报告认证错误
9. IF GitLab API 请求超时或网络不可达, THEN THE GitLab_Client SHALL 记录错误日志并将连接状态标记为 "Disconnected"

### 需求 2：ConfigMap YAML 解析与验证

**用户故事：** 作为运维工程师，我希望系统能正确解析和验证 ConfigMap YAML 文件，以便确保只有合法的 ConfigMap 配置才会被同步到集群。

#### 验收标准

1. WHEN 接收到 YAML 文件内容时, THE YAML_Parser SHALL 将文件内容解析为 ConfigMap 对象
2. WHEN YAML 文件包含合法的 Kubernetes ConfigMap 定义（apiVersion、kind、metadata.name、data 字段）时, THE YAML_Parser SHALL 返回解析成功的 ConfigMap 对象
3. WHEN YAML 文件内容不符合 Kubernetes ConfigMap 格式时, THE YAML_Parser SHALL 返回包含具体错误位置和原因的错误信息
4. WHEN YAML 文件包含语法错误时, THE YAML_Parser SHALL 返回包含行号和错误描述的解析错误
5. THE YAML_Printer SHALL 将 ConfigMap 对象格式化输出为合法的 YAML 文本
6. FOR ALL 合法的 ConfigMap 对象, 先通过 YAML_Printer 格式化再通过 YAML_Parser 解析 SHALL 产生与原始对象等价的结果（往返一致性）

### 需求 3：正向同步（GitLab → K8s）

**用户故事：** 作为运维工程师，我希望系统支持自动、定时和手动三种同步模式将 GitLab 上的 YAML 变更应用到 K8s 集群，以便根据不同场景灵活选择同步策略。

#### 验收标准

##### 通用同步流程

1. WHEN Sync_Engine 执行一次 Forward_Sync 时, THE Sync_Engine SHALL 从 GitLab 拉取所有有变更的 YAML 文件，经 YAML_Parser 验证后通过 K8s_Client 应用到目标集群
2. WHEN 用户发送 `POST /api/v1/forward-sync/{namespace}/{name}` 请求时, THE Sync_Engine SHALL 仅同步指定的 ConfigMap 变更到目标集群
3. WHEN K8s_Client 成功将 ConfigMap 应用到集群时, THE Sync_Engine SHALL 将该 ConfigMap 的同步状态更新为 "Synced"
4. IF YAML_Parser 对待同步的文件验证失败, THEN THE Sync_Engine SHALL 跳过该文件的同步并记录验证错误
5. IF K8s_Client 与 Kubernetes API Server 通信失败, THEN THE Sync_Engine SHALL 以指数退避策略重试同步操作，最多重试 3 次
6. IF 重试 3 次后同步仍然失败, THEN THE Sync_Engine SHALL 将同步状态标记为 "Failed" 并记录错误日志

##### 自动同步模式（Auto）

7. WHILE Sync_Mode 配置为 "auto", THE Sync_Engine SHALL 在收到 Webhook_Receiver 的变更通知后立即执行一次 Forward_Sync
8. WHILE Sync_Mode 配置为 "auto", THE Sync_Engine SHALL 仅同步 Webhook 事件中涉及的变更文件，而非全量同步
9. IF Webhook_Receiver 在短时间内收到多次 Push Event, THEN THE Sync_Engine SHALL 合并这些事件并仅执行一次 Forward_Sync

##### 定时同步模式（Scheduled）

10. WHILE Sync_Mode 配置为 "scheduled", THE Sync_Engine SHALL 按配置的 Sync_Interval 周期性从 GitLab 拉取最新 YAML 文件并检查变更
11. WHILE Sync_Mode 配置为 "scheduled" 且检测到 GitLab 有新变更, THE Sync_Engine SHALL 自动执行一次 Forward_Sync
12. WHILE Sync_Mode 配置为 "scheduled" 且检测到 GitLab 无新变更, THE Sync_Engine SHALL 跳过本次同步并等待下一个周期

##### 手动同步模式（Manual）

13. WHILE Sync_Mode 配置为 "manual", THE Sync_Engine SHALL 仅在用户发送 `POST /api/v1/forward-sync` 请求时执行 Forward_Sync
14. WHILE Sync_Mode 配置为 "manual" 且 Sync_Engine 检测到 GitLab 有待同步的变更, THE Sync_Engine SHALL 将这些变更标记为 "Pending" 状态，等待用户手动触发同步
15. WHEN 用户在 Manual_Sync 模式下触发 Forward_Sync 前, THE Sync_Engine SHALL 向用户展示待同步的变更差异（diff），包含变更字段和变更前后的值

### 需求 4：反向漂移检测与提示（K8s → GitLab）

**用户故事：** 作为运维工程师，我希望当 K8s 集群中的 ConfigMap 被直接修改时系统能及时提示我，由我决定是否将变更回写到 GitLab，以便保持 GitLab 作为配置的权威来源。

#### 验收标准

1. WHILE Drift_Detector 处于运行状态, THE Drift_Detector SHALL 按配置的间隔周期从 K8s 集群读取所有被管理 ConfigMap 的 Actual_State
2. WHEN Drift_Detector 检测到某个 ConfigMap 的 Actual_State 与 GitLab 中的 Desired_State 不一致时, THE Drift_Detector SHALL 生成一条 Drift_Alert，包含 ConfigMap 名称、命名空间、差异字段和检测时间
3. WHEN 存在未处理的 Drift_Alert 时, THE Sync_Engine SHALL 通过 `GET /api/v1/drift-alerts` 接口向用户展示所有未处理的漂移告警列表
4. WHEN 用户发送 `POST /api/v1/reverse-sync/{namespace}/{name}` 请求时, THE Sync_Engine SHALL 将 K8s 集群中该 ConfigMap 的 Actual_State 通过 GitLab_Client 回写到 GitLab 仓库
5. WHEN 用户发送 `POST /api/v1/drift-alerts/{id}/dismiss` 请求时, THE Sync_Engine SHALL 将该 Drift_Alert 标记为 "Dismissed"，不执行反向同步
6. WHEN Reverse_Sync 成功将 Actual_State 回写到 GitLab 时, THE Sync_Engine SHALL 将对应的 Drift_Alert 标记为 "Resolved" 并更新 Desired_State 缓存
7. IF GitLab_Client 回写操作失败, THEN THE Sync_Engine SHALL 保留 Drift_Alert 为 "Pending" 状态并记录错误日志

### 需求 5：同步历史记录

**用户故事：** 作为运维工程师，我希望系统能保存所有 ConfigMap 同步操作的历史记录，以便审计和排查问题。

#### 验收标准

1. WHEN 一次 Forward_Sync 或 Reverse_Sync 操作完成时, THE History_Store SHALL 创建一条 Sync_Record，包含以下字段：时间戳、ConfigMap 名称、命名空间、同步方向（forward 或 reverse）、变更类型、同步状态、变更前内容摘要、变更后内容摘要
2. THE History_Store SHALL 以 JSON 格式将 Sync_Record 持久化存储到本地文件系统
3. WHEN 查询同步历史时, THE History_Store SHALL 支持按 ConfigMap 名称、命名空间、同步方向和时间范围进行过滤查询
4. WHEN 查询同步历史时, THE History_Store SHALL 按时间戳降序返回 Sync_Record 列表
5. IF 写入 Sync_Record 到存储失败, THEN THE History_Store SHALL 记录错误日志并将该记录缓存在内存中，待存储恢复后重新写入

### 需求 6：配置管理

**用户故事：** 作为运维工程师，我希望能通过配置文件灵活配置系统的运行参数，以便适应不同的部署环境。

#### 验收标准

1. THE Sync_Engine SHALL 支持通过 YAML 配置文件指定以下参数：GitLab 仓库地址、GitLab 访问令牌、目标分支名称、仓库中 YAML 文件路径、Kubernetes 集群连接信息、目标命名空间、Drift 检测间隔、历史记录存储路径、HTTP API 服务端口、Sync_Mode（同步模式）、Sync_Interval（定时同步间隔）、Webhook Secret Token（Webhook 签名验证密钥）
2. WHEN 配置文件中缺少必填参数（GitLab 仓库地址、GitLab 访问令牌）时, THE Sync_Engine SHALL 在启动时报告缺失的参数名称并以非零退出码终止
3. WHERE 用户未提供配置文件, THE Sync_Engine SHALL 使用默认配置值：目标分支为 "main"、仓库中 YAML 文件路径为根目录、使用默认 kubeconfig 路径、目标命名空间为 "default"、Drift 检测间隔为 60 秒、历史记录存储路径为 `~/.configmap-sync/history`、HTTP API 服务端口为 8080、Sync_Mode 为 "manual"、Sync_Interval 为 300 秒
4. WHEN Sync_Mode 配置为 "auto" 且未提供 Webhook Secret Token 时, THE Sync_Engine SHALL 在启动时报告缺失的 Webhook Secret Token 并以非零退出码终止
5. WHEN Sync_Mode 配置为 "scheduled" 且 Sync_Interval 小于 30 秒时, THE Sync_Engine SHALL 在启动时报告 Sync_Interval 不合法并以非零退出码终止
6. IF Sync_Mode 配置值不是 "auto"、"scheduled" 或 "manual" 之一, THEN THE Sync_Engine SHALL 在启动时报告 Sync_Mode 不合法并以非零退出码终止

### 需求 7：状态查询与操作接口

**用户故事：** 作为运维工程师，我希望能通过 HTTP API 查询系统状态、触发同步操作和管理漂移告警，以便集成到现有的运维工具链中。

#### 验收标准

1. THE Sync_Engine SHALL 在配置的端口上提供 HTTP REST API 服务
2. WHEN 收到 `GET /api/v1/configmaps` 请求时, THE Sync_Engine SHALL 返回当前管理的所有 ConfigMap 列表，包含名称、命名空间、同步状态和最近一次同步时间
3. WHEN 收到 `GET /api/v1/configmaps/{namespace}/{name}` 请求时, THE Sync_Engine SHALL 返回指定 ConfigMap 的详细信息，包含 Desired_State（来自 GitLab）和 Actual_State（来自 K8s）的对比
4. WHEN 收到 `GET /api/v1/history` 请求时, THE Sync_Engine SHALL 返回同步历史记录列表，支持 `name`、`namespace`、`direction`、`since`、`until` 查询参数
5. WHEN 收到 `GET /api/v1/drift-alerts` 请求时, THE Sync_Engine SHALL 返回所有未处理的 Drift_Alert 列表
6. WHEN 收到 `POST /api/v1/check-gitlab` 请求时, THE Sync_Engine SHALL 触发一次 GitLab 变更检查并返回检查结果
7. IF 请求的 ConfigMap 不存在, THEN THE Sync_Engine SHALL 返回 HTTP 404 状态码和描述性错误消息
8. IF 请求参数格式不合法, THEN THE Sync_Engine SHALL 返回 HTTP 400 状态码和参数错误描述


### 需求 8：Web 前端界面

**用户故事：** 作为运维工程师，我希望通过一个 Web 前端界面来管理 ConfigMap 同步操作、查看 YAML 内容和对比差异，以便更直观高效地完成日常运维工作。

#### 验收标准

##### ConfigMap 列表页面

1. WHEN 用户访问 ConfigMap 列表页面时, THE Web_UI SHALL 展示所有被管理的 ConfigMap 列表，每条记录包含名称、命名空间、同步状态和最近同步时间
2. WHEN ConfigMap 的同步状态发生变化时, THE Web_UI SHALL 在用户刷新页面或自动轮询后更新对应记录的同步状态显示

##### ConfigMap 详情与 YAML 对比页面

3. WHEN 用户点击某个 ConfigMap 进入详情页面时, THE Web_UI SHALL 展示该 ConfigMap 的 Desired_State（来自 GitLab）和 Actual_State（来自 K8s）的完整 YAML 内容
4. THE YAML_Diff_View SHALL 以并排（side-by-side）方式展示 Desired_State 和 Actual_State 的 YAML 内容，并高亮显示存在差异的行
5. THE Web_UI SHALL 对所有 YAML 内容展示提供语法高亮渲染

##### 同步操作

6. WHEN 用户在 ConfigMap 列表页面点击"全量同步"按钮时, THE Web_UI SHALL 调用 `POST /api/v1/forward-sync` 接口触发全量正向同步，并展示同步结果
7. WHEN 用户在 ConfigMap 详情页面点击"同步此 ConfigMap"按钮时, THE Web_UI SHALL 调用 `POST /api/v1/forward-sync/{namespace}/{name}` 接口触发该 ConfigMap 的正向同步，并展示同步结果
8. WHEN 用户在 ConfigMap 列表页面点击"检查 GitLab 变更"按钮时, THE Web_UI SHALL 调用 `POST /api/v1/check-gitlab` 接口触发 GitLab 变更检查，并展示检查结果

##### 漂移告警页面

9. WHEN 用户访问漂移告警页面时, THE Web_UI SHALL 展示所有未处理的 Drift_Alert 列表，每条记录包含 ConfigMap 名称、命名空间、差异字段和检测时间
10. WHEN 用户点击某条 Drift_Alert 的"反向同步"按钮时, THE Web_UI SHALL 调用 `POST /api/v1/reverse-sync/{namespace}/{name}` 接口触发反向同步，并在成功后将该告警从列表中移除
11. WHEN 用户点击某条 Drift_Alert 的"忽略"按钮时, THE Web_UI SHALL 调用 `POST /api/v1/drift-alerts/{id}/dismiss` 接口忽略该告警，并将该告警从列表中移除

##### 同步历史页面

12. WHEN 用户访问同步历史页面时, THE Web_UI SHALL 展示同步历史记录列表，按时间戳降序排列
13. THE Web_UI SHALL 支持按 ConfigMap 名称、命名空间、同步方向和时间范围对同步历史记录进行过滤查询

##### 静态文件服务

14. THE Static_File_Server SHALL 通过 Go embed 包将前端构建产物嵌入到后端二进制文件中，使系统以单一二进制文件方式部署
15. WHEN 用户通过浏览器访问系统根路径时, THE Static_File_Server SHALL 返回前端 SPA 的 index.html 页面
16. WHEN 用户在 SPA 中刷新非根路径页面时, THE Static_File_Server SHALL 将未匹配 `/api/` 前缀的请求回退到 index.html，以支持前端路由

##### 错误处理

17. IF Web_UI 调用后端 API 返回错误响应, THEN THE Web_UI SHALL 在页面上展示包含错误状态码和错误描述的提示信息
18. IF Web_UI 与后端 API 通信超时或网络不可达, THEN THE Web_UI SHALL 在页面上展示网络连接错误提示，并提供重试按钮
