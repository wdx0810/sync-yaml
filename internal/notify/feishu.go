package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SyncNotification holds the data for a sync completion notification.
type SyncNotification struct {
	TaskName    string
	Direction   string
	Total       int
	Synced      int
	Failed      int
	Skipped     int
	SyncedNames []string
	FailedNames []string
	Errors      []string
	Timestamp   time.Time
}

// SendFeishu sends a notification to a Feishu group via webhook.
func SendFeishu(webhookURL string, n *SyncNotification) error {
	if webhookURL == "" {
		return nil
	}

	// Build message content.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📦 **YAML Sync 同步通知**\n\n"))
	sb.WriteString(fmt.Sprintf("**任务**: %s\n", n.TaskName))
	direction := "K8s → GitLab"
	if n.Direction == "forward" {
		direction = "GitLab → K8s"
	}
	sb.WriteString(fmt.Sprintf("**方向**: %s\n", direction))
	sb.WriteString(fmt.Sprintf("**时间**: %s\n", n.Timestamp.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("**结果**: 总计 %d，同步 %d，跳过 %d，失败 %d\n", n.Total, n.Synced, n.Skipped, n.Failed))

	if len(n.SyncedNames) > 0 {
		sb.WriteString("\n**更新资源**:\n")
		max := 15
		if len(n.SyncedNames) < max {
			max = len(n.SyncedNames)
		}
		for _, name := range n.SyncedNames[:max] {
			sb.WriteString(fmt.Sprintf("• %s\n", name))
		}
		if len(n.SyncedNames) > 15 {
			sb.WriteString(fmt.Sprintf("• ... 等共 %d 个\n", len(n.SyncedNames)))
		}
	}

	if len(n.FailedNames) > 0 {
		sb.WriteString("\n**失败资源**:\n")
		max := 5
		if len(n.FailedNames) < max {
			max = len(n.FailedNames)
		}
		for _, name := range n.FailedNames[:max] {
			sb.WriteString(fmt.Sprintf("• ❌ %s\n", name))
		}
	}

	if len(n.Errors) > 0 && n.Failed > 0 {
		sb.WriteString("\n**错误详情**:\n")
		max := 3
		if len(n.Errors) < max {
			max = len(n.Errors)
		}
		for _, e := range n.Errors[:max] {
			sb.WriteString(fmt.Sprintf("• %s\n", e))
		}
	}

	// Feishu webhook message format.
	payload := map[string]interface{}{
		"msg_type": "interactive",
		"card": map[string]interface{}{
			"header": map[string]interface{}{
				"title": map[string]interface{}{
					"tag":     "plain_text",
					"content": "YAML Sync 同步通知",
				},
				"template": func() string {
					if n.Failed > 0 {
						return "red"
					}
					if n.Synced > 0 {
						return "green"
					}
					return "blue"
				}(),
			},
			"elements": []interface{}{
				map[string]interface{}{
					"tag":     "markdown",
					"content": sb.String(),
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal feishu payload: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("send feishu webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("feishu webhook returned %d", resp.StatusCode)
	}
	return nil
}
