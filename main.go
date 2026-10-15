package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"github.com/staparx/go_showstart/monitor"
	"github.com/staparx/go_showstart/vars"
	"go.uber.org/zap"
)

func main() {
	// 用于结束程序
	defer func() {
		fmt.Println("Press Enter to exit...")
		fmt.Scanln()
	}()
	ctx := context.Background()

	//初始化日志
	log.InitLogger()

	var err error

	vars.ShowPortal()

	//初始化时间地区
	vars.TimeLocal, err = time.LoadLocation(vars.TimeLoadLocation)
	if err != nil {
		log.Logger.Error("⚠️ 初始化时间地区失败，正在使用手动定义的时区信息", zap.Error(err))
		vars.TimeLocal = time.FixedZone("CST", 8*3600)
		log.Logger.Info("✅ 手动定义的时区信息成功！!")
	} else {
		log.Logger.Info(fmt.Sprintf("✅ 时间地区 %s 初始化成功！!", vars.TimeLoadLocation))
	}

	// 打印当前系统时间
	log.Logger.Info(fmt.Sprintf("⏰ 当前系统时间：%s", time.Now().Format("2006-01-02 15:04:05")))

	cfg, err := config.InitCfg()
	if err != nil {
		log.Logger.Error("❌ 配置信息读取失败：", zap.Error(err))
		return
	}
	log.Logger.Info("✅ 系统初始化配置完成！")

	// 测试模式：测试观演人匹配功能
	if os.Getenv("TEST_PERFORMER") == "true" {
		testActivityID := os.Getenv("TEST_ACTIVITY_ID")
		testTicketID := os.Getenv("TEST_TICKET_ID")

		if testActivityID == "" || testTicketID == "" {
			fmt.Println("❌ 测试模式需要设置环境变量：")
			fmt.Println("   TEST_ACTIVITY_ID=<活动ID>")
			fmt.Println("   TEST_TICKET_ID=<票务ID>")
			fmt.Println("\n示例：")
			fmt.Println("   TEST_PERFORMER=true TEST_ACTIVITY_ID=123456 TEST_TICKET_ID=abc123 ./showstart_monitor")
			return
		}

		var activityID int
		if _, err := fmt.Sscanf(testActivityID, "%d", &activityID); err != nil {
			log.Logger.Error("活动ID格式错误", zap.String("activityId", testActivityID), zap.Error(err))
			return
		}

		log.Logger.Info("🧪 进入测试模式：测试观演人匹配功能")
		if err := monitor.TestPerformerMatching(ctx, cfg, activityID, testTicketID); err != nil {
			log.Logger.Error("测试失败", zap.Error(err))
			os.Exit(1)
		}
		log.Logger.Info("✅ 测试完成")
		return
	}

	if cfg.Monitor != nil && cfg.Monitor.Enable {
		service, err := monitor.NewService(ctx, cfg)
		if err != nil {
			log.Logger.Error("❌ 初始化监控服务失败", zap.Error(err))
			return
		}

		// 检查是否为单次运行模式（GitHub Actions）
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			log.Logger.Info("🤖 GitHub Actions 单次监控模式")
			if err := service.RunOnce(ctx); err != nil {
				log.Logger.Error("监控执行失败", zap.Error(err))
				os.Exit(1)
			}
			log.Logger.Info("✅ 监控检查完成")
			return
		}

		log.Logger.Info("👍 开始进入监控模式，按 Ctrl+C 退出")

		// 创建可取消的上下文
		monitorCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// 在 goroutine 中运行监控服务
		errChan := make(chan error, 1)
		go func() {
			errChan <- service.Run(monitorCtx)
		}()

		// 等待信号或错误
		stopChan := make(chan os.Signal, 1)
		signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

		select {
		case <-stopChan:
			log.Logger.Info("⚠️ 接收到关闭信号，正在停止监控...")
			cancel()
			<-errChan // 等待服务完全停止
		case err := <-errChan:
			if err != nil {
				log.Logger.Error("监控服务异常结束", zap.Error(err))
			}
		}

		return
	}

	log.Logger.Info("👍开始进入到票务系统抢票流程！！！")
	validate := NewValidateService(ctx, cfg)
	buyTicketList, err := validate.ValidateSystem(ctx)
	if err != nil {
		log.Logger.Error("❌ 配置信息校验失败！！！程序结束", zap.Error(err))
		return
	}

	cancelCtx, cancel := context.WithCancel(ctx)

	// 获取有效账号列表
	accounts := cfg.GetEffectiveAccounts()
	if len(accounts) == 0 {
		log.Logger.Error("❌ 未找到有效的账号配置")
		return
	}

	log.Logger.Info(fmt.Sprintf("📋 检测到 %d 个抢票账号", len(accounts)))
	for i, acc := range accounts {
		log.Logger.Info(fmt.Sprintf("  [%d] %s - 观演人: %v", i+1, acc.Name, acc.People))
	}

	// 为每个账号启动抢票流程
	for _, ticket := range buyTicketList {
		order := &OrderDetail{
			ActivityName: ticket.ActivityName,
			SessionName:  ticket.SessionName,
			Price:        ticket.Ticket.SellingPrice,
			ActivityID:   cfg.Ticket.ActivityId,
			GoodType:     ticket.Ticket.GoodType,
			TicketID:     ticket.Ticket.TicketID,
		}

		// 多账号模式：为每个账号启动独立的抢票协程
		for _, account := range accounts {
			log.Logger.Info(fmt.Sprintf("[%s] 🎫 准备抢票: %s", account.Name, ticket.ActivityName))
			err = ConfirmOrderForAccount(cancelCtx, order, account, cfg)
			if err != nil {
				log.Logger.Error(fmt.Sprintf("[%s] ❌ 抢票准备失败", account.Name), zap.Error(err))
				// 单个账号失败不影响其他账号
				continue
			}
		}
	}

	// 捕获终止信号
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	// 监听多账号结果和单账号结果
	select {
	case result := <-GlobalResultChan:
		cancel()
		if result.Success {
			log.Logger.Info(fmt.Sprintf("🎉 [%s] 抢票成功！赶紧去订单页面支付吧！！🎉", result.AccountName))
			// 下单成功，发送邮件提醒
			if cfg.SmtpEmail.Enable && result.Order != nil {
				subject := vars.GetEmailTitle()
				body := fmt.Sprintf("[账号: %s]\n%s", result.AccountName,
					vars.GetEmailFormat(result.Order.ActivityName, result.Order.SessionName, result.Order.Price))
				if err := sendEmail(subject, body, cfg); err != nil {
					log.Logger.Error("发送邮件失败：", zap.Error(err))
				} else {
					log.Logger.Info("下单成功，邮件已发送")
				}
			}
		} else {
			log.Logger.Error(fmt.Sprintf("❌ [%s] 抢票失败", result.AccountName), zap.Error(result.Error))
			if cfg.SmtpEmail.Enable {
				subject := fmt.Sprintf("[%s] 抢票失败", result.AccountName)
				body := fmt.Sprintf("错误信息：%v", result.Error)
				if err := sendEmail(subject, body, cfg); err != nil {
					log.Logger.Error("发送邮件失败：", zap.Error(err))
				}
			}
		}
	case order := <-channel:
		// 向后兼容：单账号模式的结果通道
		cancel()
		log.Logger.Info("🎉抢票成功！赶紧去订单页面支付吧！！🎉")
		if cfg.SmtpEmail.Enable {
			subject := vars.GetEmailTitle()
			body := vars.GetEmailFormat(order.ActivityName, order.SessionName, order.Price)
			if err := sendEmail(subject, body, cfg); err != nil {
				log.Logger.Error("发送邮件失败：", zap.Error(err))
			} else {
				log.Logger.Info("下单成功，邮件已发送")
			}
		}
	case Error := <-ErrorChannel:
		// 向后兼容：单账号模式的错误通道
		cancel()
		log.Logger.Error("❌ 抢票失败！！！程序结束")
		if cfg.SmtpEmail.Enable {
			subject := "抢票初始化失败，请查看错误，并及时处理重启程序！！！"
			body := fmt.Sprintf("错误信息：%s", Error.Error())
			if err := sendEmail(subject, body, cfg); err != nil {
				log.Logger.Error("发送邮件失败：", zap.Error(err))
			} else {
				log.Logger.Info("下单失败，邮件已发送")
			}
		}
	case <-stopChan:
		log.Logger.Info("⚠️ 接收到关闭信号，程序关闭")
		cancel()
		return
	}
}
