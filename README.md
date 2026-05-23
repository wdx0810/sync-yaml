# YAML Sync

GitLab 与 Kubernetes 之间的双向 YAML 同步工具。把 GitOps 的"声明式"和 K8s 的"运行时"连起来：从 GitLab 把 YAML 应用到集群、把集群里的资源回写到 GitLab，全部带审核流程。

## 特性

### 同步能力
- **正向同步（GitLab → K8s）**：递归扫描 GitLab 仓库目录下的所有 `.yaml/.yml`，通过 K8s Server-Side Apply 应用到目标集群
- **反向同步（K8s → GitLab）**：列出 K8s 集群资源，清理掉运行时字段后写回 GitLab，**单次同步只产生一个 commit**
- **审核流程**：正向同步前展示所有待变更资源的 YAML diff，用户逐项勾选后才真正应用
- **多资源类型**：ConfigMap、Secret、Deployment、StatefulSet、DaemonSet、Service、Ingress、Job、CronJob、PVC、RBAC 等都支持
- **多命名空间**：单个任务可同时同步多个命名空间，逗号分隔
- **三种触发模式**：手动、定时（cron 间隔）、自动（监听 K8s Watch 事件）

### 用户与权限
- 多用户管理（管理员 / 普通用户）
- 任务级权限：查看 / 同步 / 编辑/删除，三档独立
- 项目级权限：按 `project` 字段批量授权一组任务，任务级优先于项目级
- TOTP 二次认证（兼容 Google Authenticator、Microsoft Authenticator、1Password 等）
- 管理员可远程开关 / 重置任意用户的 MFA

### 数据安全
- 数据源 token、kubeconfig 落盘前用 AES-GCM 加密
- 用户密码、MFA secret 不出 API（响应里被屏蔽）
- 持久化目录可以挂卷或外挂存储

## 截图

页面包含：登录（含 MFA 步骤）、仪表盘、数据源、同步任务、同步预览、同步历史、用户与权限、安全认证。

## 快速开始

### 默认账号

```
用户名：admin
密码：  admin123
```

首次登录建议立即修改密码并启用 MFA。

### Docker 部署

**方式一：用预编译二进制（推荐，构建最快）**

仓库根目录已经包含 `Dockerfile.prebuilt`。先在你的开发机交叉编译一份 Linux 二进制：

```bash
# 编译前端
cd web && npm install && npm run build && cd ..
# 把前端产物嵌入后端编译路径
rm -rf cmd/server/web/dist && cp -r web/dist cmd/server/web/dist
# 交叉编译 Linux 二进制
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o server-linux-amd64 ./cmd/server
```

然后直接打镜像（30 秒内完成）：

```bash
docker build -f Dockerfile.prebuilt -t yaml-sync:latest .
```

**方式二：在容器内编译（不需要本地 Go / Node 环境）**

```bash
docker build -t yaml-sync:latest .
```

**运行**

```bash
docker run -d \
  --name yaml-sync \
  -p 8080:8080 \
  -v yaml-sync-data:/data \
  yaml-sync:latest
```

打开 http://主机IP:8080

### Kubernetes 部署

`deploy/k8s.yaml` 是参考清单：Deployment + Service + PersistentVolumeClaim。把里面 `image:` 字段改成你推送到镜像仓库后的实际地址，然后：

```bash
kubectl apply -f deploy/k8s.yaml
```

注意持久化卷必须保留，里面存着所有数据源、任务、用户、加密密钥。

## 数据持久化

容器内 `/data` 目录持久化以下文件：

| 文件 | 内容 |
|------|------|
| `auth.json` | 兼容旧版认证配置（已弃用，新版用 users.json） |
| `users.json` | 用户列表，含 password、role、mfa secret |
| `permissions.json` | 用户对任务/项目的权限映射 |
| `sources.json` | GitLab 数据源（token 加密） |
| `targets.json` | K8s 集群目标（kubeconfig 加密） |
| `sync_tasks.json` | 同步任务定义 |
| `encryption.key` | 用于加解密上面三类敏感字段的密钥 |
| `history.jsonl` | 同步历史记录（追加写） |

## 权限模型

| 角色 | 看任务 | 同步任务 | 编辑/删除任务 | 管理用户 |
|------|--------|----------|---------------|----------|
| admin | 全部 | 全部 | 全部 | ✓ |
| user（默认）| 无 | 无 | 无 | × |
| user + 任务级授权 | 视权限 | 视权限 | 视权限 | × |
| user + 项目级授权 | 该项目下所有任务 | 视权限 | 视权限 | × |

权限解析顺序：**任务级 → 项目级**。即任务级显式设置过的，优先生效；没设置才回落到项目级。

## API 概览

完整路由见 `internal/api/api.go`。常用：

```
POST   /api/v1/auth/login            登录（可能要求 MFA 二步）
POST   /api/v1/auth/mfa/verify       MFA 校验
GET    /api/v1/dashboard             仪表盘
GET    /api/v1/sources               数据源列表
GET    /api/v1/targets               K8s 集群列表
GET    /api/v1/tasks                 同步任务列表
POST   /api/v1/tasks/{id}/preview    正向同步预览（返回变更列表）
POST   /api/v1/tasks/{id}/apply      应用已批准的变更
POST   /api/v1/tasks/{id}/sync       直接同步（反向同步直接执行）
GET    /api/v1/users                 用户管理（admin 专属）
PUT    /api/v1/users/{name}/permissions/{taskId}        设置任务权限
PUT    /api/v1/users/{name}/project-permissions/{proj}  设置项目权限
PUT    /api/v1/users/{name}/mfa-enabled                 远程开关 MFA
```

## 从源码构建

需要 Go 1.22+ 和 Node 20+。

```bash
# 前端
cd web
npm install
npm run build
cd ..

# 嵌入产物
rm -rf cmd/server/web/dist
cp -r web/dist cmd/server/web/dist

# 后端
go build -o server ./cmd/server
./server
```

服务默认监听 `:8080`，数据写到 `/data`（Linux）。Windows 跑 `server.exe` 时数据会写到 `/data` 绝对路径，需要确保该目录存在或修改 `internal/config/config.go` 默认值。

## 开发说明

```
.
├── cmd/server/          二进制入口，embed 前端 dist
├── internal/
│   ├── api/             HTTP handler、路由、中间件
│   ├── config/          配置加载
│   ├── crypto/          AES-GCM 加密服务
│   ├── diff/            YAML 对比器
│   ├── drift/           漂移检测（旧版 ConfigMap-only 流程，已废弃）
│   ├── engine/          同步引擎核心（GenericSyncer + TaskManager）
│   ├── gitlab/          GitLab API 客户端
│   ├── history/         同步历史持久化
│   ├── k8s/             K8s 客户端（含 dynamic、cleaner、gvr resolver）
│   ├── parser/          YAML parser（generic 是新版，根 parser 是旧版）
│   ├── path/            GitLab 内文件路径生成 / 反解析
│   ├── store/           数据持久化（sources、targets、tasks、users、permissions）
│   └── webhook/         GitLab webhook 接入
└── web/                 React + Vite + Ant Design 前端
    └── src/pages/       各页面
```

## 故障排查

**同步报"网络连接错误"**
- 看浏览器 F12 → Network 里那条请求的状态码和响应体
- 看服务端日志（容器 stdout）有没有 panic 或 error
- 确认 K8s API server 和 GitLab 都能从服务端容器内访问到（容器内 `curl -k -m 5 https://k8s-api/version`）

**反向同步后再正向同步报"field is immutable"**
- 升级到最新版即可。`internal/k8s/cleaner` 会剥离 Service.clusterIP、ServiceAccount.secrets、PVC.volumeName 等运行时字段
- 如果你 GitLab 里已经有"脏" YAML，可以编辑掉相关字段后再同步

**MFA 验证总是失败**
- 检查服务端机器和手机的系统时间是否同步（误差 30 秒以内）
- 让管理员在用户管理页"清除密钥"，重新扫码

## 协议

内部使用，未指定开源协议。
