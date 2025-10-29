package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Notifier struct {
	webhooks []string
	alerts   []string
}

func NewNotifier(webhookURL, alertURL string) *Notifier {
	return &Notifier{
		webhooks: splitWebhooks(webhookURL),
		alerts:   splitWebhooks(alertURL),
	}
}

func splitWebhooks(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	res := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			res = append(res, part)
		}
	}
	return res
}

func (n *Notifier) Send(message string) error {
	if len(n.webhooks) == 0 {
		return fmt.Errorf("未配置 webhook_url")
	}

	payload := map[string]interface{}{
		"msg_type": "text",
		"content":  map[string]string{"text": message},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// 发送到所有 webhook
	var lastErr error
	for _, webhook := range n.webhooks {
		// 每次循环都创建新的 Reader
		resp, err := http.Post(webhook, "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("webhook 返回异常状态: %d", resp.StatusCode)
		}
	}

	return lastErr
}

// SendStructured 发送结构化通知（支持 Echobell 模板）
func (n *Notifier) SendStructured(eventType, artist, title, showTime, siteName, activityURL string) error {
	if len(n.webhooks) == 0 {
		return fmt.Errorf("未配置 webhook_url")
	}

	// 使用 Echobell 模板变量
	payload := map[string]interface{}{
		"type":     eventType,   // "new" 或 "timed"
		"artist":   artist,      // 艺人名称
		"title":    title,       // 演出标题
		"showTime": showTime,    // 演出时间
		"siteName": siteName,    // 场馆名称
		"url":      activityURL, // 演出链接（可选）
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// 发送到所有 webhook
	var lastErr error
	for _, webhook := range n.webhooks {
		// 每次循环都创建新的 Reader
		resp, err := http.Post(webhook, "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("webhook 返回异常状态: %d", resp.StatusCode)
		}
	}

	return lastErr
}

func (n *Notifier) SendAlert(message string) error {
	if len(n.alerts) == 0 {
		return nil
	}

	payload := map[string]interface{}{
		"msg_type": "text",
		"content": map[string]string{
			"text": message,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	var lastErr error
	for _, webhook := range n.alerts {
		resp, err := http.Post(webhook, "application/json", bytes.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("alert webhook 返回异常状态: %d", resp.StatusCode)
		}
	}

	return lastErr
}
