package store

import (
	"context"
	"fmt"
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use the K8s client interface to list namespaces.
	// We need to access the underlying clientset — use ListConfigMaps as a connectivity test.
	_, err = kc.ListConfigMaps(ctx, "default")
	if err != nil {
		// Try to give a clearer error.
		return fmt.Errorf("K8s connection test failed: %w", err)
	}
	return nil
}
