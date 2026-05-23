# 部署文档

## 项目结构

```
├── cmd/server/           # Go 后端入口
│   ├── main.go
│   └── web/dist/         # 前端构建产物（嵌入到二进制）
├── internal/             # Go 后端业务代码
│   ├── api/              # HTTP API + 认证 + MFA
│   ├── config/           # 配置管理
│   ├── crypto/           # AES 加密
│   ├── diff/             # 资源差异比较
│   ├── drift/            # 漂移检测
│   ├── engine/           # 同步引擎 + 任务管理
│   ├── gitlab/           # GitLab 客户端
│   ├── history/          # 同步历史
│   ├── k8s/              # K8s 客户端 + Dynamic Client + GVR
│   ├── parser/           # YAML 解析器（通用资源）
│   ├── path/             # 文件路径生成
│   ├── store/            # 数据持久化（源/目标/任务）
│   └── webhook/          # Webhook 接收
├── web/                  # 前端源码（React + TypeScript + Vite）
│   ├── src/
│   ├── package.json
│   └── vite.config.ts
├── go.mod
└── go.sum
```

## 环境要求

| 工具 | 版本 | 用途 |
|------|------|------|
| Go | 1.22+ | 后端编译 |
| Node.js | 18+ | 前端构建 |
| npm | 9+ | 前端依赖管理 |

## 编译步骤

### 1. 安装前端依赖并构建

```bash
cd web
npm install
npx vite build
```

构建产物输出到 `web/dist/`。

### 2. 复制前端产物到后端 embed 目录

```bash
# Windows
xcopy /E /Y web\dist cmd\server\web\dist\

# Linux/Mac
cp -r web/dist cmd/server/web/dist
```

### 3. 编译后端二进制

```bash
# Windows
set GOPROXY=https://goproxy.cn,direct
go build -o yaml-sync.exe ./cmd/server/...

# Linux
GOPROXY=https://goproxy.cn,direct go build -o yaml-sync ./cmd/server/...

# 交叉编译 Linux（在 Windows 上）
set GOOS=linux
set GOARCH=amd64
set GOPROXY=https://goproxy.cn,direct
go build -o yaml-sync ./cmd/server/...
```

### 4. 一键编译脚本（Windows）

```powershell
# build.ps1
Set-Location web
npm install
npx vite build
Set-Location ..
Remove-Item -Recurse -Force cmd\server\web\dist -ErrorAction SilentlyContinue
Copy-Item -Recurse web\dist cmd\server\web\dist
$env:GOPROXY = "https://goproxy.cn,direct"
go build -o yaml-sync.exe ./cmd/server/...
Write-Host "编译完成: yaml-sync.exe"
```

### 5. 一键编译脚本（Linux/Mac）

```bash
#!/bin/bash
# build.sh
cd web && npm install && npx vite build && cd ..
rm -rf cmd/server/web/dist
cp -r web/dist cmd/server/web/dist
GOPROXY=https://goproxy.cn,direct go build -o yaml-sync ./cmd/server/...
echo "编译完成: yaml-sync"
```

## 运行

```bash
# 直接运行（默认端口 8080）
./yaml-sync

# 指定配置文件
./yaml-sync -config /path/to/config.yaml

# 后台运行（Linux）
nohup ./yaml-sync > /var/log/yaml-sync.log 2>&1 &
```

## 配置文件（可选）

```yaml
# config.yaml
sync:
  mode: manual        # auto, scheduled, manual
  interval: 300       # 定时同步间隔（秒）
drift:
  interval: 60        # 漂移检测间隔（秒）
history:
  storagePath: /data
api:
  port: 8080
```

不提供配置文件时使用默认值。

## 数据存储

所有数据存储在 `/data` 目录下：

```
/data/
├── encryption.key          # AES 加密密钥（自动生成）
├── auth.json               # 认证配置（含 MFA）
├── gitlab_sources.json     # GitLab 连接配置
├── k8s_targets.json        # K8s 集群配置
├── sync_tasks.json         # 同步任务配置
└── history.json            # 同步历史记录
```

## 访问

- 地址：http://localhost:8080
- 默认账号：admin
- 默认密码：admin123

## Docker 部署

```bash
# 构建镜像
docker build -t yaml-sync:latest .

# 运行
docker run -d -p 8080:8080 -v /data/yaml-sync:/data --name yaml-sync yaml-sync:latest
```

## Kubernetes 部署

```bash
kubectl apply -f deploy/k8s.yaml
```

## systemd 服务（Linux）

```ini
# /etc/systemd/system/yaml-sync.service
[Unit]
Description=YAML Sync Service
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/yaml-sync
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable yaml-sync
systemctl start yaml-sync
```
