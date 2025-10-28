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
}

func NewNotifier(webhookURL string) *Notifier {
	// 支持多个 webhook，用逗号分隔
	var webhooks []string
	if webhookURL != "" {
		for _, url := range strings.Split(webhookURL, ",") {
			url = strings.TrimSpace(url)
			if url != "" {
				webhooks = append(webhooks, url)
			}
		}
	}
	return &Notifier{webhooks: webhooks}
}

func (n *Notifier) Send(message string) error {
	if len(n.webhooks) == 0 {
		return fmt.Errorf("未配置 webhook_url")
	}
	
	payload := map[string]interface{}{
		"msg_type": "text",
		"content": map[string]string{"text": message},
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
		"type":     eventType,    // "new" 或 "timed"
		"artist":   artist,       // 艺人名称
		"title":    title,        // 演出标题
		"showTime": showTime,     // 演出时间
		"siteName": siteName,     // 场馆名称
		"url":      activityURL,  // 演出链接（可选）
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



