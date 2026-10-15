package monitor

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"github.com/staparx/go_showstart/vars"
	"go.uber.org/zap"
)

// PurchaseService 自动抢票服务
type PurchaseService struct {
	client   client.ShowStartIface
	cfg      *config.Config
	notifier *Notifier
	running  sync.Map // 记录正在抢票的活动ID，避免重复启动
}

// OrderDetail 订单详情
type OrderDetail struct {
	ActivityName string
	SessionName  string
	Price        string
	ActivityID   int
	GoodType     int
	TicketID     string
}

func NewPurchaseService(client client.ShowStartIface, cfg *config.Config, notifier *Notifier) *PurchaseService {
	return &PurchaseService{
		client:   client,
		cfg:      cfg,
		notifier: notifier,
	}
}

// StartAutoPurchase 启动自动抢票
func (ps *PurchaseService) StartAutoPurchase(ctx context.Context, activityID int, activityTitle string) {
	activityIDStr := fmt.Sprintf("%d", activityID)

	// 检查是否已经在抢票
	if _, exists := ps.running.LoadOrStore(activityIDStr, true); exists {
		log.Logger.Info("该活动已在抢票中，跳过", zap.String("activityId", activityIDStr))
		return
	}

	// 在 goroutine 中执行抢票，不阻塞监控流程
	go func() {
		defer ps.running.Delete(activityIDStr)

		if err := ps.purchaseTicket(ctx, activityID, activityTitle); err != nil {
			log.Logger.Error("自动抢票失败",
				zap.String("activityId", activityIDStr),
				zap.String("title", activityTitle),
				zap.Error(err))
			// 失败不通知，直接返回
			return
		}
	}()
}

// purchaseTicket 执行抢票逻辑
func (ps *PurchaseService) purchaseTicket(ctx context.Context, activityID int, activityTitle string) error {
	log.Logger.Info("开始自动抢票",
		zap.Int("activityId", activityID),
		zap.String("title", activityTitle))

	// 1. 获取票务信息（不需要活动详情，直接获取票务信息即可）
	ticketList, err := ps.client.ActivityTicketList(ctx, activityID)
	if err != nil {
		return fmt.Errorf("获取票务信息失败: %w", err)
	}

	if len(ticketList.Result) == 0 {
		return fmt.Errorf("未找到可用票务")
	}

	// 3. 选择最高价票务
	ticketInfo, sessionName, err := ps.selectHighestPriceTicket(ticketList.Result)
	if err != nil {
		return fmt.Errorf("选择票务失败: %w", err)
	}

	log.Logger.Info("已选择票务",
		zap.String("sessionName", sessionName),
		zap.String("ticketId", ticketInfo.TicketID),
		zap.String("price", ticketInfo.SellingPrice))

	// 输出便于测试的信息
	fmt.Printf("\n" + strings.Repeat("=", 60) + "\n")
	fmt.Printf("🎫 已选择票务（可用于测试）\n")
	fmt.Printf("活动ID: %d\n", activityID)
	fmt.Printf("票务ID: %s\n", ticketInfo.TicketID)
	fmt.Printf("场次: %s\n", sessionName)
	fmt.Printf("票价: %s\n", ticketInfo.SellingPrice)
	fmt.Printf("\n测试命令：\n")
	fmt.Printf("TEST_PERFORMER=true TEST_ACTIVITY_ID=%d TEST_TICKET_ID=%s ./showstart_monitor\n",
		activityID, ticketInfo.TicketID)
	fmt.Printf(strings.Repeat("=", 60) + "\n\n")

	// 4. 随机选择1-3个观演人
	people := ps.selectRandomPeople()
	if len(people) == 0 {
		return fmt.Errorf("未配置观演人信息")
	}

	log.Logger.Info("已选择观演人", zap.Strings("people", people))

	// 5. 获取开始时间
	startTime := time.Unix(ticketInfo.StartTime/1000, (ticketInfo.StartTime%1000)*1000000).In(vars.TimeLocal)
	startTimeStr := startTime.Format("2006-01-02 15:04:05.000")

	log.Logger.Info("定时购开始时间", zap.String("startTime", startTimeStr))

	// 6. 启动抢票流程
	orderDetail := &OrderDetail{
		ActivityName: activityTitle,
		SessionName:  sessionName,
		Price:        ticketInfo.SellingPrice,
		ActivityID:   activityID,
		GoodType:     ticketInfo.GoodType,
		TicketID:     ticketInfo.TicketID,
	}

	// 创建临时的 ticket 配置用于抢票
	tempCfg := *ps.cfg
	tempCfg.Ticket = &config.Ticket{
		ActivityId: activityID,
		StartTime:  startTimeStr,
		List:       []config.TicketList{{Session: sessionName, Price: ticketInfo.SellingPrice}},
		People:     people,
	}

	// 调用抢票逻辑
	return ps.confirmOrder(ctx, orderDetail, &tempCfg)
}

// selectHighestPriceTicket 选择最高价票务
func (ps *PurchaseService) selectHighestPriceTicket(sessions []*client.ActivityTicket) (*client.TicketInfo, string, error) {
	var highestTicket *client.TicketInfo
	var highestSessionName string
	var highestPrice float64 = -1

	for _, session := range sessions {
		for _, priceGroup := range session.TicketPriceList {
			for _, ticket := range priceGroup.TicketList {
				// 只选择可购买的票务（SaleStatus == 1 表示可购买）
				if ticket.SaleStatus != 1 || ticket.RemainTicket <= 0 {
					continue
				}

				price, err := strconv.ParseFloat(ticket.SellingPrice, 64)
				if err != nil {
					log.Logger.Warn("解析票价失败", zap.String("price", ticket.SellingPrice), zap.Error(err))
					continue
				}

				if price > highestPrice {
					highestPrice = price
					highestTicket = ticket
					highestSessionName = session.SessionName
				}
			}
		}
	}

	if highestTicket == nil {
		return nil, "", fmt.Errorf("未找到可购买的票务")
	}

	return highestTicket, highestSessionName, nil
}

// selectRandomPeople 随机选择1-3个观演人
func (ps *PurchaseService) selectRandomPeople() []string {
	people := ps.cfg.Monitor.AutoPurchasePeople
	if len(people) == 0 {
		return nil
	}

	// 随机选择1-3个
	count := rand.Intn(3) + 1 // 1-3
	if count > len(people) {
		count = len(people)
	}

	// 打乱顺序并选择前count个
	shuffled := make([]string, len(people))
	copy(shuffled, people)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled[:count]
}

// confirmOrder 确认订单并启动抢票
func (ps *PurchaseService) confirmOrder(ctx context.Context, order *OrderDetail, cfg *config.Config) error {
	num := len(cfg.Ticket.People)

	// 订单信息确认
	confirm, err := ps.client.Confirm(ctx, order.ActivityID, order.TicketID, fmt.Sprintf("%d", num))
	if err != nil {
		log.Logger.Error("订单信息确认失败", zap.Error(err))
		return err
	}

	log.Logger.Info("订单信息确认成功", zap.String("ticket_id", order.TicketID))

	pay := strconv.FormatFloat(confirm.Result.OrderInfoVo.TicketPriceVo.Price*float64(num), 'f', 2, 64)

	// 下单请求
	orderReq := &client.OrderReq{
		OrderDetails: []*client.OrderDetail{
			{
				GoodsType:  order.GoodType,
				SkuType:    confirm.Result.OrderInfoVo.TicketPriceVo.TicketType,
				Num:        fmt.Sprintf("%d", num),
				GoodsID:    confirm.Result.OrderInfoVo.ActivityID,
				SkuID:      confirm.Result.OrderInfoVo.TicketPriceVo.TicketID,
				Price:      confirm.Result.OrderInfoVo.TicketPriceVo.Price,
				GoodsPhoto: confirm.Result.OrderInfoVo.Poster,
				DyPOIType:  confirm.Result.OrderInfoVo.TicketPriceVo.DyPOIType,
				GoodsName:  confirm.Result.OrderInfoVo.Title,
			},
		},
		CommonPerfomerIds: []int{},
		AreaCode:          confirm.Result.OrderInfoVo.AreaCode,
		Telephone:         confirm.Result.OrderInfoVo.Telephone,
		AddressID:         "",
		TeamID:            "",
		CouponID:          "",
		CheckCode:         "",
		Source:            0,
		Discount:          0,
		SessionID:         confirm.Result.OrderInfoVo.SessionID,
		Freight:           0,
		AmountPayable:     pay,
		TotalAmount:       pay,
		Partner:           "",
		OrderSource:       1,
		VideoID:           "",
		PayVideotype:      "",
		StFlpv:            "",
		Sign:              "",
		TrackPath:         "",
	}

	// 是否需要查询观演人
	if vars.NeedCpMap[confirm.Result.OrderInfoVo.BuyType] {
		log.Logger.Info("票务类型需要观演人信息", zap.Int("buyType", confirm.Result.OrderInfoVo.BuyType))

		cpResp, err := ps.client.CpList(ctx, order.TicketID)
		if err != nil {
			log.Logger.Error("查询观演人信息失败", zap.Error(err))
			return err
		}

		// 记录所有可用的观演人信息（用于调试和验证）
		log.Logger.Info("查询到的观演人列表",
			zap.Int("total", len(cpResp.Result)),
			zap.Any("performers", func() []map[string]interface{} {
				var list []map[string]interface{}
				for _, v := range cpResp.Result {
					list = append(list, map[string]interface{}{
						"id":                 v.ID,
						"name":               v.Name,
						"documentType":       v.DocumentType,
						"documentTypeStr":    v.DocumentTypeStr,
						"showDocumentNumber": v.ShowDocumentNumber,
						"canBuy":             v.CanBuy,
					})
				}
				return list
			}()))

		var perfomerIds []int
		var matchedNames []string
		for _, v := range cpResp.Result {
			for _, user := range cfg.Ticket.People {
				if v.Name == user {
					perfomerIds = append(perfomerIds, v.ID)
					matchedNames = append(matchedNames, fmt.Sprintf("%s(ID:%d,证件:%s)", v.Name, v.ID, v.ShowDocumentNumber))
					break
				}
			}
		}

		if len(perfomerIds) > 0 && len(perfomerIds) == len(cfg.Ticket.People) {
			log.Logger.Info("观演人信息匹配成功",
				zap.Strings("matched", matchedNames),
				zap.Ints("performerIds", perfomerIds))
			orderReq.CommonPerfomerIds = perfomerIds
		} else {
			log.Logger.Error("观演人信息匹配失败",
				zap.Strings("configured", cfg.Ticket.People),
				zap.Strings("matched", matchedNames),
				zap.Int("expected", len(cfg.Ticket.People)),
				zap.Int("actual", len(perfomerIds)))
			return fmt.Errorf("观演人信息匹配失败：配置了 %d 个观演人，但只匹配到 %d 个", len(cfg.Ticket.People), len(perfomerIds))
		}
	}

	// 是否需要填写地址
	if vars.NeedAdress[confirm.Result.OrderInfoVo.TicketPriceVo.TicketType] {
		log.Logger.Info("票务类型需要地址信息", zap.Int("ticketType", confirm.Result.OrderInfoVo.TicketPriceVo.TicketType))

		adressList, err := ps.client.AdressList(ctx)
		if err != nil {
			log.Logger.Error("查询地址信息失败", zap.Error(err))
			return err
		}

		if len(adressList.Result) > 0 {
			for _, v := range adressList.Result {
				if v.IsDefault == 1 {
					orderReq.AddressID = strconv.Itoa(v.ID)
					log.Logger.Info("地址信息匹配成功", zap.String("address", v.Address))
					break
				}
			}
			if orderReq.AddressID == "" {
				log.Logger.Error("地址信息匹配失败，请设置默认地址")
				return fmt.Errorf("地址信息匹配失败，请设置默认地址")
			}
		} else {
			log.Logger.Error("地址信息匹配失败，请设置默认地址")
			return fmt.Errorf("地址信息匹配失败，请设置默认地址")
		}
	}

	// 解析开始时间
	t, err := time.ParseInLocation("2006-01-02 15:04:05.000", cfg.Ticket.StartTime, vars.TimeLocal)
	if err != nil {
		return fmt.Errorf("时间格式错误: %w", err)
	}

	log.Logger.Info("抢票启动时间", zap.String("startTime", t.Format("2006-01-02 15:04:05.000")))

	startTime := t.UnixNano() / int64(time.Millisecond)

	// 创建成功通知通道
	successChan := make(chan *OrderDetail, 1)
	errorChan := make(chan error, 1)

	// 启动抢票进程
	StartOrder := func() {
		since := (startTime - time.Now().UnixNano()/int64(time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(since) * time.Millisecond):
			log.Logger.Info("活动即将开始，开始监听抢票")
			for i := 0; i < cfg.System.MaxGoroutine; i++ {
				go ps.goOrder(ctx, i, orderReq, cfg, order, successChan, errorChan)
			}
		}
	}

	// 倒计时进程
	Countdown := func() {
		since := (startTime - time.Now().UnixNano()/int64(time.Millisecond))
		since -= 10000
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(since) * time.Millisecond):
			since = (startTime - time.Now().UnixNano()/int64(time.Millisecond))
			for since > 0 && ctx.Err() == nil {
				log.Logger.Info(fmt.Sprintf("距离抢票开始还有：%d秒", since/1000))
				time.Sleep(1 * time.Second)
				since -= 1000
			}
		}
	}

	// token 重新获取进程
	GetTokenAgain := func() {
		since := (startTime - time.Now().UnixNano()/int64(time.Millisecond))
		since -= 1000 * 60 * 3
		if since < 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(since) * time.Millisecond):
			err := ps.client.GetToken(ctx)
			if err != nil {
				log.Logger.Error("token重新获取失败", zap.Error(err))
				err = ps.client.GetToken(ctx)
				if err != nil {
					log.Logger.Error("token重新获取失败", zap.Error(err))
					errorChan <- err
					return
				}
			}
		}
	}

	go StartOrder()
	go Countdown()
	go GetTokenAgain()

	// 等待抢票结果
	select {
	case order := <-successChan:
		log.Logger.Info("🎉抢票成功！赶紧去订单页面支付吧！！")
		// 发送成功通知
		ps.notifyPurchaseSuccess(order)
		return nil
	case err := <-errorChan:
		log.Logger.Error("抢票失败", zap.Error(err))
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// goOrder 抢票执行逻辑
func (ps *PurchaseService) goOrder(ctx context.Context, index int, orderReq *client.OrderReq, cfg *config.Config, order *OrderDetail, successChan chan<- *OrderDetail, errorChan chan<- error) {
	logPrefix := fmt.Sprintf("[%d]", index)
	orderJobKeyAcquired := false
	var orderJobKeyAcquiredLock sync.Mutex

	firstLoop := index == 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if !firstLoop {
				// 随机等待
				minInterval := cfg.System.MinInterval
				maxInterval := cfg.System.MaxInterval
				interval := rand.Intn(maxInterval-minInterval+1) + minInterval
				time.Sleep(time.Duration(interval) * time.Millisecond)
			} else {
				firstLoop = false
			}

			// 获取orderJobKey锁
			orderJobKeyAcquiredLock.Lock()
			if orderJobKeyAcquired {
				orderJobKeyAcquiredLock.Unlock()
				time.Sleep(200 * time.Millisecond)
				continue
			}
			orderJobKeyAcquiredLock.Unlock()

			// 下单
			orderResp, err := ps.client.Order(ctx, orderReq)
			if err != nil {
				log.Logger.Error(logPrefix+"下单失败", zap.Error(err))
				time.Sleep(200 * time.Millisecond)
				continue
			}

			orderJobKey := orderResp.Result.OrderJobKey
			if orderJobKey == "" {
				log.Logger.Error(logPrefix + "orderJobKey为空")
				time.Sleep(200 * time.Millisecond)
				continue
			}

			log.Logger.Info(fmt.Sprintf(logPrefix+"获取orderJobKey成功！orderJobKey：%s", orderJobKey))

			// 获取orderJobKey锁
			orderJobKeyAcquiredLock.Lock()
			orderJobKeyAcquired = true
			orderJobKeyAcquiredLock.Unlock()

			OrderResult, orderResultCancel := context.WithCancel(ctx)
			defer orderResultCancel()

			// 每隔200ms发送查询订单结果
			for {
				select {
				case <-OrderResult.Done():
					return
				default:
					go func() {
						GetOrderResp, err := ps.client.GetOrderResult(ctx, orderJobKey)

						if OrderResult.Err() != nil {
							return
						}

						if err != nil {
							log.Logger.Error(logPrefix+"查询订单结果失败", zap.Error(err))
							if strings.Contains(err.Error(), "小手指点得太快啦，休息一下") {
								return
							}
							orderJobKeyAcquiredLock.Lock()
							orderJobKeyAcquired = false
							orderJobKeyAcquiredLock.Unlock()
							orderResultCancel()
							return
						}

						log.Logger.Info(fmt.Sprintf(logPrefix+"查询订单结果成功！订单号：%s", GetOrderResp.Result.OrderSn))
						orderResultCancel()
						successChan <- order
					}()
					time.Sleep(200 * time.Millisecond)
				}
			}
		}
	}
}

// notifyPurchaseSuccess 发送抢票成功通知
func (ps *PurchaseService) notifyPurchaseSuccess(order *OrderDetail) {
	// 标题：演出名称 + 票价（最关键信息，一眼就能看到）
	title := fmt.Sprintf("🎉 %s - %s元", order.ActivityName, order.Price)

	// 内容：场次信息
	content := fmt.Sprintf("场次：%s", order.SessionName)

	// 发送结构化数据，支持 Echobell 标题和内容分离
	data := map[string]interface{}{
		"title":    title,
		"content":  content,
		"activity": order.ActivityName,
		"session":  order.SessionName,
		"price":    order.Price,
	}

	if err := ps.notifier.SendStructuredPurchase(data); err != nil {
		log.Logger.Error("发送抢票成功通知失败", zap.Error(err))
	}
}
