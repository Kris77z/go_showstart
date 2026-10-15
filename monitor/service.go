package monitor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"github.com/staparx/go_showstart/vars"
	"go.uber.org/zap"
)

type Service struct {
	client          client.ShowStartIface
	state           *StateManager
	notifier        *Notifier
	cfg             *config.Monitor
	interval        time.Duration
	location        *time.Location
	purchaseService *PurchaseService // 自动抢票服务（可选）
	fullCfg         *config.Config   // 完整配置（用于自动抢票）
}

func NewService(ctx context.Context, cfg *config.Config) (*Service, error) {
	if cfg == nil || cfg.Monitor == nil || !cfg.Monitor.Enable {
		return nil, fmt.Errorf("monitor 未开启")
	}
	if cfg.Showstart == nil {
		return nil, fmt.Errorf("缺少 showstart 配置")
	}
	if cfg.Monitor.AutoPurchaseEnable {
		return nil, client.ErrUnsafeLegacySubmit
	}

	log.Logger.Info("初始化监控客户端")
	cl := client.NewShowStartClient(ctx, cfg.Showstart)
	log.Logger.Info("读取监控状态", zap.String("stateDir", cfg.Monitor.StateDir))
	state, err := NewStateManager(cfg.Monitor.StateDir)
	if err != nil {
		return nil, err
	}
	log.Logger.Info("监控状态读取完成")
	interval := time.Duration(cfg.Monitor.IntervalSecond) * time.Second
	if interval <= 0 {
		interval = 180 * time.Second
	}

	loc := vars.TimeLocal
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}

	service := &Service{
		client:   cl,
		state:    state,
		notifier: NewNotifier(cfg.Monitor.WebhookURL, cfg.Monitor.AlertWebhookURL),
		cfg:      cfg.Monitor,
		interval: interval,
		location: loc,
		fullCfg:  cfg,
	}

	// 如果启用自动抢票，初始化抢票服务（使用独立的 client 实例，避免影响监控）
	if cfg.Monitor.AutoPurchaseEnable {
		// 创建独立的 client 实例，确保抢票不影响监控的 client
		purchaseClient := client.NewShowStartClient(ctx, cfg.Showstart)
		service.purchaseService = NewPurchaseService(purchaseClient, cfg, service.notifier)
		log.Logger.Info("✅ 自动抢票功能已启用")
	}

	return service, nil
}

func (s *Service) Run(ctx context.Context) error {
	log.Logger.Info("🎯 启动秀动监控模式", zap.Int("keywords", len(s.cfg.Keywords)), zap.Duration("interval", s.interval))

	// 首次尝试刷新 token，失败不致命，后续请求会重试
	if err := s.client.GetToken(ctx); err != nil {
		log.Logger.Warn("初始化获取 token 失败，将在后续请求中重试", zap.Error(err))
	}

	s.ensureInitialized(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		if err := s.runOnce(ctx); err != nil {
			log.Logger.Error("监控轮询失败", zap.Error(err))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// RunOnce 执行单次监控检查（用于 GitHub Actions）
func (s *Service) RunOnce(ctx context.Context) error {
	log.Logger.Info("🎯 执行单次监控检查", zap.Int("keywords", len(s.cfg.Keywords)))

	// 首次尝试刷新 token，失败不致命，后续请求会重试
	if err := s.client.GetToken(ctx); err != nil {
		log.Logger.Warn("初始化获取 token 失败，将在后续请求中重试", zap.Error(err))
	}

	s.ensureInitialized(ctx)

	return s.runOnce(ctx)
}

func (s *Service) runOnce(ctx context.Context) error {
	for i, keyword := range s.cfg.Keywords {
		if err := s.monitorKeyword(ctx, keyword); err != nil {
			log.Logger.Error("监控单个关键词失败", zap.String("keyword", keyword), zap.Error(err))
		}

		// 在每个关键词之间添加延迟（除了最后一个），模拟真实搜索操作
		if i < len(s.cfg.Keywords)-1 {
			time.Sleep(1000 * time.Millisecond) // 每个关键词间隔 1 秒
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

func (s *Service) monitorKeyword(ctx context.Context, keyword string) error {
	resp, err := s.client.ActivitySearchList(ctx, s.cfg.CityCode, keyword)
	if err != nil {
		log.Logger.Error("请求演出列表失败", zap.String("keyword", keyword), zap.Error(err))
		if isAuthError(err) {
			s.alert(fmt.Sprintf("关键词 %s 演出列表请求失败：%v", keyword, err))
		}
		return err
	}

	if len(resp.Result.ActivityInfo) == 0 {
		log.Logger.Debug("关键词暂无演出", zap.String("keyword", keyword))
		return nil
	}

	normalizedKeyword := normalizeKeyword(keyword)

	for _, activity := range resp.Result.ActivityInfo {
		if activity == nil || activity.ActivityID == 0 || activity.Title == "" {
			continue
		}

		if !keywordMatches(normalizedKeyword, activity.Title) {
			continue
		}

		// 先处理新上架通知
		s.processNewActivity(activity, keyword)
		// 再处理定时购通知（如果支持定时购）
		s.processTimedActivity(ctx, activity, keyword)
	}

	return nil
}

// processNewActivity 处理新上架活动通知
func (s *Service) processNewActivity(activity *client.ActivityInfo, keyword string) {
	activityID := fmt.Sprintf("%d", activity.ActivityID)

	// 如果已经见过这个活动，跳过
	if s.state.HasSeen(activityID) {
		return
	}

	activityURL := fmt.Sprintf("https://wap.showstart.com/pages/activity/detail/detail?activityId=%d", activity.ActivityID)
	if err := s.notifier.SendStructured("new", keyword, activity.Title, activity.ShowTime, activity.SiteName, activityURL); err != nil {
		log.Logger.Error("Webhook 通知失败", zap.String("type", "new_listing"), zap.Error(err))
		return
	}

	// 记录到已见过列表
	s.state.MarkSeen(activityID)
	log.Logger.Info("发现新演出上架",
		zap.String("keyword", keyword),
		zap.String("activityId", activityID),
		zap.String("title", activity.Title))

	// 输出便于测试的信息
	fmt.Printf("\n" + strings.Repeat("=", 60) + "\n")
	fmt.Printf("🎉 发现新演出上架\n")
	fmt.Printf("活动ID: %s\n", activityID)
	fmt.Printf("活动名称: %s\n", activity.Title)
	fmt.Printf("关键词: %s\n", keyword)
	fmt.Printf(strings.Repeat("=", 60) + "\n\n")
}

func (s *Service) processTimedActivity(ctx context.Context, activity *client.ActivityInfo, keyword string) {
	activityID := fmt.Sprintf("%d", activity.ActivityID)

	if !hasTimedLabel(activity.OtherLabel) {
		return
	}

	activityURL := fmt.Sprintf("https://wap.showstart.com/pages/activity/detail/detail?activityId=%d", activity.ActivityID)
	if s.state.HasTimed(activityID) {
		return
	}

	snapshot := s.fetchTimedTicketSnapshot(ctx, activity)
	successRateText := timedNotificationSuccessRateText(snapshot)

	notifyTitle := activity.Title
	var extra map[string]interface{}
	successRateForLog := "-"
	if successRateText != "" {
		notifyTitle += "\n成功率：" + successRateText
		successRateForLog = successRateText
		extra = map[string]interface{}{
			"successRate":     formatRate(snapshot.BestPreBuyRate),
			"successRateText": successRateText,
		}
	} else {
		log.Logger.Info("定时购未返回成功率，按基础定时购推送",
			zap.String("keyword", keyword),
			zap.String("activityId", activityID),
			zap.String("title", activity.Title))
	}

	if err := s.notifier.SendStructuredWithExtra("timed", keyword, notifyTitle, activity.ShowTime, activity.SiteName, activityURL, extra); err != nil {
		log.Logger.Error("Webhook 通知失败", zap.String("type", "timed_purchase"), zap.Error(err))
		return
	}

	// 记录到定时购列表，同时确保也记录到已见过列表（避免重复通知）
	s.state.MarkTimed(activityID)
	if !s.state.HasSeen(activityID) {
		s.state.MarkSeen(activityID)
	}
	log.Logger.Info("发现定时购",
		zap.String("keyword", keyword),
		zap.String("activityId", activityID),
		zap.String("title", activity.Title),
		zap.String("successRate", successRateForLog))

	// 输出便于测试的信息
	fmt.Printf("\n" + strings.Repeat("=", 60) + "\n")
	fmt.Printf("🎯 发现定时购活动（可用于测试）\n")
	fmt.Printf("活动ID: %s\n", activityID)
	fmt.Printf("活动名称: %s\n", activity.Title)
	fmt.Printf("关键词: %s\n", keyword)
	fmt.Printf("成功率: %s\n", successRateForLog)
	fmt.Printf(strings.Repeat("=", 60) + "\n\n")

	// 如果启用自动抢票，异步启动抢票流程（完全隔离，不影响监控）
	// 注意：AutoPurchaseEnable 默认为 false，旧配置文件不包含此字段时不会触发
	if s.cfg != nil && s.cfg.AutoPurchaseEnable && s.purchaseService != nil {
		// 使用独立的 context，避免抢票的取消影响监控
		purchaseCtx := ctx
		activityIDInt, err := strconv.Atoi(activityID)
		if err == nil {
			// 使用 defer recover 确保抢票启动失败不会影响监控
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Logger.Error("自动抢票启动异常（已恢复，不影响监控）",
							zap.String("activityId", activityID),
							zap.Any("panic", r))
					}
				}()
				s.purchaseService.StartAutoPurchase(purchaseCtx, activityIDInt, activity.Title)
			}()
		} else {
			log.Logger.Warn("活动ID转换失败，跳过自动抢票", zap.String("activityId", activityID), zap.Error(err))
		}
	}
}

func (s *Service) fetchTimedTicketSnapshot(ctx context.Context, activity *client.ActivityInfo) *TimedTicketSnapshot {
	activityID := fmt.Sprintf("%d", activity.ActivityID)
	ticketList, err := s.client.ActivityTicketList(ctx, activity.ActivityID)
	if err != nil {
		log.Logger.Warn("拉取定时购票档详情失败",
			zap.String("activityId", activityID),
			zap.String("title", activity.Title),
			zap.Error(err))
		return nil
	}

	snapshot := buildTimedTicketSnapshot(activityID, ticketList, s.location)
	if len(snapshot.Tickets) == 0 {
		log.Logger.Warn("定时购票档详情为空", zap.String("activityId", activityID), zap.String("title", activity.Title))
		return nil
	}
	return snapshot
}

func (s *Service) ensureInitialized(ctx context.Context) {
	if s.state.IsInitialized() {
		return
	}

	var (
		seenIDs  []string
		timedIDs []string
	)

	for _, keyword := range s.cfg.Keywords {
		resp, err := s.client.ActivitySearchList(ctx, s.cfg.CityCode, keyword)
		if err != nil {
			log.Logger.Warn("初始化拉取演出失败", zap.String("keyword", keyword), zap.Error(err))
			if isAuthError(err) {
				s.alert(fmt.Sprintf("初始化失败：关键词 %s 拉取异常：%v", keyword, err))
			}
			continue
		}

		normalizedKeyword := normalizeKeyword(keyword)
		for _, activity := range resp.Result.ActivityInfo {
			if activity == nil || activity.ActivityID == 0 || activity.Title == "" {
				continue
			}
			if !keywordMatches(normalizedKeyword, activity.Title) {
				continue
			}
			id := fmt.Sprintf("%d", activity.ActivityID)
			seenIDs = append(seenIDs, id)
			if hasTimedLabel(activity.OtherLabel) {
				timedIDs = append(timedIDs, id)
			}
		}
	}

	s.state.BatchMark(seenIDs, timedIDs)
	s.state.MarkInitialized()
	log.Logger.Info("监控状态初始化完成", zap.Int("seen", len(seenIDs)), zap.Int("timed", len(timedIDs)))
}

func (s *Service) alert(message string) {
	if err := s.notifier.SendAlert(message); err != nil {
		log.Logger.Warn("告警发送失败", zap.Error(err))
	}
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "登录") || strings.Contains(msg, "login") || strings.Contains(msg, "token") || strings.Contains(msg, "unauthorized")
}

func normalizeKeyword(input string) string {
	return strings.TrimSpace(strings.ToLower(removeSpecialChars(input)))
}

func keywordMatches(normalizedKeyword string, title string) bool {
	titleNormalized := strings.ToLower(removeSpecialChars(title))
	return strings.Contains(titleNormalized, normalizedKeyword)
}

func removeSpecialChars(input string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Han, r) {
			return unicode.ToLower(r)
		}
		return -1
	}, input)
}

func hasTimedLabel(labels []*client.OtherLabel) bool {
	for _, label := range labels {
		if label != nil && label.Name == "支持定时购票" {
			return true
		}
	}
	return false
}
