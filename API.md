# YAML Sync API 文档

Base URL: `http://<host>:8080/api/v1`

## 认证方式

除特别标注外，所有接口需要 Bearer Token 认证：
```
Authorization: Bearer <login-token>
```

登录获取 token：`POST /api/v1/auth/login`

---

## 认证

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/auth/login` | 登录 |
| GET | `/auth/check` | 检查登录状态 |
| POST | `/auth/change-password` | 修改密码 |
| POST | `/auth/mfa/verify` | MFA 二次验证 |
| GET | `/auth/mfa/status` | 当前用户 MFA 状态 |
| POST | `/auth/mfa/setup` | 生成 MFA 密钥 |
| POST | `/auth/mfa/enable` | 启用 MFA |
| POST | `/auth/mfa/disable` | 禁用 MFA |

---

## 数据源

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/sources` | 列出所有 GitLab 数据源 |
| POST | `/sources` | 创建 GitLab 数据源 |
| PUT | `/sources/{name}` | 更新数据源 |
| DELETE | `/sources/{name}` | 删除数据源 |
| POST | `/sources/{name}/test` | 测试连接 |

---

## K8s 集群

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/targets` | 列出所有 K8s 集群 |
| POST | `/targets` | 创建 K8s 集群 |
| PUT | `/targets/{name}` | 更新集群 |
| DELETE | `/targets/{name}` | 删除集群 |
| POST | `/targets/{name}/test` | 测试连接 |

---

## 通知渠道

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/notify-channels` | 列出所有通知渠道 |
| POST | `/notify-channels` | 创建通知渠道 |
| PUT | `/notify-channels/{name}` | 更新通知渠道 |
| DELETE | `/notify-channels/{name}` | 删除通知渠道 |

**创建请求体：**
```json
{
  "name": "研发群",
  "type": "feishu",
  "webhookUrl": "https://open.feishu.cn/open-apis/bot/v2/hook/xxxxxxxx"
}
```

---

## 同步任务

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/tasks` | 列出所有同步任务 |
| POST | `/tasks` | 创建任务 |
| PUT | `/tasks/{id}` | 更新任务 |
| DELETE | `/tasks/{id}` | 删除任务 |
| POST | `/tasks/{id}/start` | 启动任务 |
| POST | `/tasks/{id}/pause` | 暂停任务 |
| POST | `/tasks/{id}/sync` | 手动触发同步 |
| POST | `/tasks/{id}/preview` | 预览变更（正向同步） |
| POST | `/tasks/{id}/apply` | 应用已审核的变更 |
| POST | `/tasks/{id}/webhook-token` | 生成/重新生成 Webhook Token |
| DELETE | `/tasks/{id}/webhook-token` | 删除 Webhook Token |

**创建任务请求体：**
```json
{
  "name": "UAT反向同步",
  "project": "NTSP",
  "sourceName": "gitlab-main",
  "targetName": "uat-cluster",
  "sourcePath": "/UAT/ntsp/",
  "targetNamespace": "ntsp",
  "direction": "reverse",
  "syncMode": "manual",
  "interval": 300,
  "resourceTypes": ["Deployment", "Service", "ConfigMap"],
  "includeFilter": "lion-.*",
  "excludeFilter": "system:.*",
  "notifyChannel": "研发群"
}
```

**Apply 请求体：**
```json
{
  "changes": [
    {
      "kind": "Deployment",
      "namespace": "ntsp",
      "name": "my-app",
      "action": "updated",
      "oldYAML": "...",
      "newYAML": "...",
      "apiVersion": "apps/v1",
      "namespaced": true
    }
  ]
}
```

---

## 外部 Webhook 触发（Token 认证，无需登录）

| 方法 | 路径 | 认证方式 | 说明 |
|------|------|---------|------|
| POST | `/hooks/sync/{id}?token=xxx` | Token 参数 | 触发同步 |
| POST | `/hooks/sync/{id}` + `Authorization: Bearer xxx` | Token Header | 触发同步 |

**使用流程：**
1. 先在系统里（或调 API）生成 Token：`POST /tasks/{id}/webhook-token`
2. 外部系统调用时带上 Token

**curl 示例：**
```bash
curl -X POST "http://your-server:8080/api/v1/hooks/sync/task-123456?token=a1b2c3d4e5f6..."
```

**返回：**
```json
{
  "total": 930,
  "synced": 5,
  "failed": 0,
  "skipped": 925,
  "syncedNames": ["ntsp/my-app (Deployment)", "..."]
}
```

---

## 同步历史

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/history?page=1&pageSize=20` | 分页查询历史 |
| GET | `/history/{id}` | 获取单条记录详情（含 YAML diff） |

**查询参数：**
- `page` - 页码（默认 1）
- `pageSize` - 每页条数（默认 50）
- `name` - 按资源名过滤
- `namespace` - 按命名空间过滤
- `direction` - `forward` 或 `reverse`
- `since` - 起始时间（RFC3339）
- `until` - 结束时间（RFC3339）

---

## 用户管理（Admin）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/users` | 列出所有用户 |
| POST | `/users` | 创建用户 |
| GET | `/users/me` | 当前用户信息 |
| GET | `/users/{username}` | 获取用户 |
| PUT | `/users/{username}` | 更新用户 |
| DELETE | `/users/{username}` | 删除用户 |
| GET | `/users/{username}/permissions` | 获取用户权限 |
| PUT | `/users/{username}/permissions/{taskId}` | 设置任务权限 |
| DELETE | `/users/{username}/permissions/{taskId}` | 删除任务权限 |
| PUT | `/users/{username}/project-permissions/{project}` | 设置项目权限 |
| DELETE | `/users/{username}/project-permissions/{project}` | 删除项目权限 |
| POST | `/users/{username}/mfa-reset` | 重置用户 MFA |
| PUT | `/users/{username}/mfa-enabled` | 开关用户 MFA |

**创建用户：**
```json
{
  "username": "zhangsan",
  "password": "123456",
  "role": "user",
  "enabled": true
}
```

**设置权限：**
```json
{
  "canView": true,
  "canSync": true,
  "canEdit": false
}
```

---

## 仪表盘

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/dashboard` | 仪表盘数据（任务统计+列表） |
| GET | `/status` | 系统状态 |
