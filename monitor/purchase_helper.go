package monitor

import (
	"context"
	"fmt"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"go.uber.org/zap"
)

// TestPerformerMatching 测试观演人匹配功能
// 使用方法：设置环境变量 TEST_PERFORMER=true TEST_ACTIVITY_ID=<活动ID> TEST_TICKET_ID=<票务ID>
func TestPerformerMatching(ctx context.Context, cfg *config.Config, activityID int, ticketID string) error {
	log.Logger.Info("开始测试观演人匹配功能",
		zap.Int("activityId", activityID),
		zap.String("ticketId", ticketID),
		zap.Strings("configuredPeople", cfg.Monitor.AutoPurchasePeople))

	// 创建客户端
	cl := client.NewShowStartClient(ctx, cfg.Showstart)

	// 获取 token
	if err := cl.GetToken(ctx); err != nil {
		return fmt.Errorf("获取 token 失败: %w", err)
	}

	// 查询观演人列表
	cpResp, err := cl.CpList(ctx, ticketID)
	if err != nil {
		return fmt.Errorf("查询观演人信息失败: %w", err)
	}

	// 显示所有可用的观演人
	fmt.Println("\n========== 系统中所有可用的观演人 ==========")
	for i, v := range cpResp.Result {
		fmt.Printf("%d. 姓名: %s\n", i+1, v.Name)
		fmt.Printf("   ID: %d\n", v.ID)
		fmt.Printf("   证件类型: %s (%d)\n", v.DocumentTypeStr, v.DocumentType)
		fmt.Printf("   证件号码: %s\n", v.ShowDocumentNumber)
		fmt.Printf("   可购买: %d\n", v.CanBuy)
		fmt.Println()
	}

	// 测试匹配
	fmt.Println("========== 配置的观演人 ==========")
	for i, name := range cfg.Monitor.AutoPurchasePeople {
		fmt.Printf("%d. %s\n", i+1, name)
	}

	fmt.Println("\n========== 匹配结果 ==========")
	var matched []string
	var matchedIDs []int
	for _, configuredName := range cfg.Monitor.AutoPurchasePeople {
		found := false
		for _, performer := range cpResp.Result {
			if performer.Name == configuredName {
				matched = append(matched, fmt.Sprintf("✓ %s (ID:%d, 证件:%s)", performer.Name, performer.ID, performer.ShowDocumentNumber))
				matchedIDs = append(matchedIDs, performer.ID)
				found = true
				break
			}
		}
		if !found {
			matched = append(matched, fmt.Sprintf("✗ %s (未找到)", configuredName))
		}
	}

	for _, m := range matched {
		fmt.Println(m)
	}

	if len(matchedIDs) == len(cfg.Monitor.AutoPurchasePeople) {
		fmt.Println("\n✅ 所有观演人匹配成功！")
		return nil
	} else {
		fmt.Printf("\n❌ 匹配失败：配置了 %d 个观演人，但只匹配到 %d 个\n", len(cfg.Monitor.AutoPurchasePeople), len(matchedIDs))
		return fmt.Errorf("观演人匹配失败")
	}
}
