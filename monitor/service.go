package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"go.uber.org/zap"
)

type Service struct {
	client   client.ShowStartIface
	state    *StateManager
	notifier *Notifier
	cfg      *config.Monitor
	interval time.Duration
}

func NewService(ctx context.Context, cfg *config.Config) (*Service, error) {
	if cfg == nil || cfg.Monitor == nil || !cfg.Monitor.Enable {
		return nil, fmt.Errorf("monitor 未开启")
	}
	if cfg.Showstart == nil {
		return nil, fmt.Errorf("缺少 showstart 配置")
	}

	cl := client.NewShowStartClient(ctx, cfg.Showstart)
	state, err := NewStateManager(cfg.Monitor.StateDir)
	if err != nil {
		return nil, err
	}
	notifier := NewNotifier(cfg.Monitor.WebhookURL)
	interval := time.Duration(cfg.Monitor.IntervalSecond) * time.Second
	if interval <= 0 {
		interval = 180 * time.Second
	}

	return &Service{
		client:   cl,
		state:    state,
		notifier: notifier,
		cfg:      cfg.Monitor,
		interval: interval,
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	log.Logger.Info("🎯 启动秀动监控模式", zap.Int("keywords", len(s.cfg.Keywords)), zap.Duration("interval", s.interval))

	// 首次尝试刷新 token，失败不致命，后续请求会重试
	if err := s.client.GetToken(ctx); err != nil {
		log.Logger.Warn("初始化获取 token 失败，将在后续请求中重试", zap.Error(err))
	}

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

	return s.runOnce(ctx)
}

func (s *Service) runOnce(ctx context.Context) error {
	for _, keyword := range s.cfg.Keywords {
		if err := s.monitorKeyword(ctx, keyword); err != nil {
			log.Logger.Error("监控单个关键词失败", zap.String("keyword", keyword), zap.Error(err))
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
		return err
	}

	if len(resp.Result.ActivityInfo) == 0 {
		log.Logger.Debug("关键词暂无演出", zap.String("keyword", keyword))
		return nil
	}

	kwLower := strings.ToLower(keyword)
	for _, activity := range resp.Result.ActivityInfo {
		if activity == nil || activity.ActivityID == 0 || activity.Title == "" {
			continue
		}

		if !strings.Contains(strings.ToLower(activity.Title), kwLower) {
			continue
		}

		s.handleNewEvent(activity, keyword)
		s.handleTimedPurchase(activity, keyword)
	}

	return nil
}

func (s *Service) handleNewEvent(activity *client.ActivityInfo, keyword string) {
	activityID := fmt.Sprintf("%d", activity.ActivityID)
	if s.state.HasSeen(activityID) {
		return
	}

	// 构造演出链接
	activityURL := fmt.Sprintf("https://wap.showstart.com/pages/activity/detail/detail?activityId=%d", activity.ActivityID)
	
	// 使用结构化通知（支持 Echobell 模板）
	if err := s.notifier.SendStructured("new", keyword, activity.Title, activity.ShowTime, activity.SiteName, activityURL); err != nil {
		log.Logger.Error("Webhook 通知失败", zap.String("type", "new_event"), zap.Error(err))
		return
	}

	s.state.MarkSeen(activityID)
	log.Logger.Info("发现新演出", zap.String("keyword", keyword), zap.String("activityId", activityID), zap.String("title", activity.Title))
}

func (s *Service) handleTimedPurchase(activity *client.ActivityInfo, keyword string) {
	activityID := fmt.Sprintf("%d", activity.ActivityID)
	if s.state.HasTimed(activityID) {
		return
	}

	for _, label := range activity.OtherLabel {
		if label != nil && label.Name == "支持定时购票" {
			// 构造演出链接
			activityURL := fmt.Sprintf("https://wap.showstart.com/pages/activity/detail/detail?activityId=%d", activity.ActivityID)
			
			// 使用结构化通知（支持 Echobell 模板）
			if err := s.notifier.SendStructured("timed", keyword, activity.Title, activity.ShowTime, activity.SiteName, activityURL); err != nil {
				log.Logger.Error("Webhook 通知失败", zap.String("type", "timed_purchase"), zap.Error(err))
				return
			}
			s.state.MarkTimed(activityID)
			log.Logger.Info("发现定时购", zap.String("keyword", keyword), zap.String("activityId", activityID), zap.String("title", activity.Title))
			return
		}
	}
}

