package monitor

import (
	"context"
	"fmt"
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
	client   client.ShowStartIface
	state    *StateManager
	notifier *Notifier
	cfg      *config.Monitor
	interval time.Duration
	location *time.Location
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
	interval := time.Duration(cfg.Monitor.IntervalSecond) * time.Second
	if interval <= 0 {
		interval = 180 * time.Second
	}

	loc := vars.TimeLocal
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}

	return &Service{
		client:   cl,
		state:    state,
		notifier: NewNotifier(cfg.Monitor.WebhookURL, cfg.Monitor.AlertWebhookURL),
		cfg:      cfg.Monitor,
		interval: interval,
		location: loc,
	}, nil
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
		log.Logger.Error("请求演出列表失败", zap.String("keyword", keyword), zap.Error(err))
		s.alert(fmt.Sprintf("关键词 %s 演出列表请求失败：%v", keyword, err))
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

		s.processTimedActivity(activity, keyword)
	}

	return nil
}

func (s *Service) processTimedActivity(activity *client.ActivityInfo, keyword string) {
	activityID := fmt.Sprintf("%d", activity.ActivityID)

	if !hasTimedLabel(activity.OtherLabel) {
		return
	}

	if s.state.HasTimed(activityID) {
		return
	}

	activityURL := fmt.Sprintf("https://wap.showstart.com/pages/activity/detail/detail?activityId=%d", activity.ActivityID)
	if err := s.notifier.SendStructured("timed", keyword, activity.Title, activity.ShowTime, activity.SiteName, activityURL); err != nil {
		log.Logger.Error("Webhook 通知失败", zap.String("type", "timed_purchase"), zap.Error(err))
		s.alert(fmt.Sprintf("告警：通知发送失败，关键词=%s，演出=%s，错误=%v", keyword, activity.Title, err))
		return
	}

	s.state.BatchMark([]string{activityID}, []string{activityID})
	log.Logger.Info("发现定时购", zap.String("keyword", keyword), zap.String("activityId", activityID), zap.String("title", activity.Title))
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
			s.alert(fmt.Sprintf("初始化失败：关键词 %s 拉取异常：%v", keyword, err))
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
