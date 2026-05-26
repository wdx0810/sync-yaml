package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	gogitlab "github.com/xanzy/go-gitlab"

	"github.com/configmap-sync/configmap-sync/internal/k8s"
)

type ConnectionTester interface {
	TestGitLab(url, token string, projectID int) error
	TestK8s(kubeconfigContent string) error
}

type connectionTester struct{}

func NewConnectionTester() ConnectionTester {
	return &connectionTester{}
}

func (c *connectionTester) TestGitLab(url, token string, projectID int) error {
	client, err := gogitlab.NewClient(token, gogitlab.WithBaseURL(url))
	if err != nil {
		return fmt.Errorf("failed to create GitLab client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err = client.Projects.GetProject(projectID, nil, gogitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("GitLab connection test failed: %w", err)
	}
	return nil
}

func (c *connectionTester) TestK8s(kubeconfigContent string) error {
	if kubeconfigContent == "" {
		return fmt.Errorf("kubeconfig content is empty")
	}

	// Use the same manual parser as NewClientFromContent — no base64 validation.
	kc, err := k8s.NewClientFromContent(kubeconfigContent)
	if err != nil {
		return fmt.Errorf("failed to create K8s client: %w", err)
	}

	// 30s — public-internet API servers (e.g. cross-region clouds) may need
	// more than 10s for the first TLS handshake. The previous 10s frequently
	// timed out before the TCP/TLS handshake completed.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use ListConfigMaps as a connectivity test.
	_, err = kc.ListConfigMaps(ctx, "default")
	if err != nil {
		return classifyK8sConnError(err)
	}
	return nil
}

// classifyK8sConnError turns a raw error into a more actionable message.
func classifyK8sConnError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "i/o timeout"):
		return fmt.Errorf("K8s 连接超时：30 秒内未连上 API server。请检查 1) kubeconfig 里 server 地址在 yaml-sync 容器里是否可达 2) 集群安全组/防火墙是否放行了 6443 端口 3) 是否在内网而当前服务跑在公网。原始错误：%w", err)
	case strings.Contains(msg, "x509"),
		strings.Contains(msg, "certificate"):
		return fmt.Errorf("K8s TLS 证书校验失败：%w", err)
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("K8s 拒绝连接：API server 端口未开放或服务未启动。原始错误：%w", err)
	case strings.Contains(msg, "no such host"),
		strings.Contains(msg, "dns"):
		return fmt.Errorf("K8s 域名解析失败：%w", err)
	case strings.Contains(msg, "Unauthorized"),
		strings.Contains(msg, "401"):
		return fmt.Errorf("K8s 鉴权失败：token 无效或已过期。原始错误：%w", err)
	case strings.Contains(msg, "forbidden"),
		strings.Contains(msg, "403"):
		return fmt.Errorf("K8s 权限不足：当前账号没有 list configmaps 权限。原始错误：%w", err)
	}
	return fmt.Errorf("K8s connection test failed: %w", err)
}
