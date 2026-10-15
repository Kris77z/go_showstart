package monitor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/staparx/go_showstart/client"
)

type TimedTicketSnapshot struct {
	ActivityID     string               `json:"activityId"`
	Summary        string               `json:"summary"`
	BestPreBuyRate *float64             `json:"bestPreBuyRate,omitempty"`
	Tickets        []TimedTicketInsight `json:"tickets"`
}

type TimedTicketInsight struct {
	SessionName          string   `json:"sessionName"`
	SessionID            int      `json:"sessionId"`
	TicketID             string   `json:"ticketId"`
	TicketType           string   `json:"ticketType"`
	Price                string   `json:"price"`
	SaleStatus           int      `json:"saleStatus"`
	SaleStatusText       string   `json:"saleStatusText"`
	RemainTicket         int      `json:"remainTicket"`
	LimitBuyNum          int      `json:"limitBuyNum"`
	CanBuyNum            int      `json:"canBuyNum"`
	UnPayOrderNum        int      `json:"unPayOrderNum"`
	Countdown            int      `json:"countdown"`
	StartTime            string   `json:"startTime,omitempty"`
	StartTimeUnixMS      int64    `json:"startTimeUnixMs,omitempty"`
	TimeText             string   `json:"timeText,omitempty"`
	Instruction          string   `json:"instruction,omitempty"`
	PreBuySuccessRate    *float64 `json:"preBuySuccessRate,omitempty"`
	PreBuyRateBand       string   `json:"preBuyRateBand,omitempty"`
	AlternateSuccessRate *float64 `json:"alternateSuccessRate,omitempty"`
	AlternateRateBand    string   `json:"alternateRateBand,omitempty"`
	WaitingAlternateNum  int      `json:"waitingAlternateNum,omitempty"`
	AlternateLimitNum    int      `json:"alternateLimitNum,omitempty"`
	AlternateContent     string   `json:"alternateContent,omitempty"`
	IsOpenAlternate      int      `json:"isOpenAlternate,omitempty"`
	CommonPerformerTips  string   `json:"commonPerformerTips,omitempty"`
}

func buildTimedTicketSnapshot(activityID string, ticketList *client.ActivityTicketListResp, loc *time.Location) *TimedTicketSnapshot {
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	if ticketList == nil {
		return &TimedTicketSnapshot{
			ActivityID: activityID,
			Summary:    "ticket/list 未返回票档。",
		}
	}

	seen := map[string]struct{}{}
	insights := make([]TimedTicketInsight, 0)
	for _, session := range ticketList.Result {
		if session == nil {
			continue
		}
		for _, priceGroup := range session.TicketPriceList {
			for _, ticket := range priceGroup.TicketList {
				insights = appendTicketInsight(insights, seen, session, ticket, loc)
			}
		}
		if len(session.TicketPriceList) == 0 {
			for _, ticket := range session.TicketList {
				insights = appendTicketInsight(insights, seen, session, ticket, loc)
			}
		}
	}

	sort.SliceStable(insights, func(i, j int) bool {
		left := sortRate(insights[i].PreBuySuccessRate)
		right := sortRate(insights[j].PreBuySuccessRate)
		if left != right {
			return left < right
		}
		leftAlt := sortRate(insights[i].AlternateSuccessRate)
		rightAlt := sortRate(insights[j].AlternateSuccessRate)
		if leftAlt != rightAlt {
			return leftAlt < rightAlt
		}
		return priceValue(insights[i].Price) > priceValue(insights[j].Price)
	})

	snapshot := &TimedTicketSnapshot{
		ActivityID: activityID,
		Tickets:    insights,
	}
	for _, ticket := range insights {
		if visibleRate(ticket.PreBuySuccessRate) {
			best := *ticket.PreBuySuccessRate
			snapshot.BestPreBuyRate = &best
			break
		}
	}
	snapshot.Summary = buildTicketSummary(insights)
	return snapshot
}

func appendTicketInsight(insights []TimedTicketInsight, seen map[string]struct{}, session *client.ActivityTicket, ticket *client.TicketInfo, loc *time.Location) []TimedTicketInsight {
	if ticket == nil {
		return insights
	}

	key := fmt.Sprintf("%d:%s", session.SessionID, ticket.TicketID)
	if _, ok := seen[key]; ok {
		return insights
	}
	seen[key] = struct{}{}

	insight := TimedTicketInsight{
		SessionName:          session.SessionName,
		SessionID:            session.SessionID,
		TicketID:             ticket.TicketID,
		TicketType:           ticket.TicketType,
		Price:                firstNonEmpty(ticket.SellingPrice, ticket.CostPrice),
		SaleStatus:           ticket.SaleStatus,
		SaleStatusText:       saleStatusText(ticket.SaleStatus),
		RemainTicket:         ticket.RemainTicket,
		LimitBuyNum:          ticket.LimitBuyNum,
		CanBuyNum:            ticket.CanBuyNum,
		UnPayOrderNum:        ticket.UnPayOrderNum,
		Countdown:            ticket.Countdown,
		StartTimeUnixMS:      ticket.StartTime,
		TimeText:             ticket.Time,
		Instruction:          strings.TrimSpace(ticket.Instruction),
		PreBuySuccessRate:    visibleRatePtr(ticket.PreBuySuccessRate),
		AlternateSuccessRate: visibleRatePtr(ticket.AlternateSuccessRate),
		WaitingAlternateNum:  ticket.WaitingAlternateNum,
		AlternateLimitNum:    ticket.AlternateLimitNum,
		AlternateContent:     strings.TrimSpace(ticket.AlternateContent),
		IsOpenAlternate:      ticket.IsOpenAlternate,
		CommonPerformerTips:  strings.TrimSpace(ticket.CommonPerformerTips),
	}
	if ticket.StartTime > 0 {
		insight.StartTime = time.UnixMilli(ticket.StartTime).In(loc).Format("2006-01-02 15:04:05")
	}
	if insight.PreBuySuccessRate != nil {
		insight.PreBuyRateBand = rateBandText(*insight.PreBuySuccessRate)
	}
	if insight.AlternateSuccessRate != nil {
		insight.AlternateRateBand = rateBandText(*insight.AlternateSuccessRate)
	}

	return append(insights, insight)
}

func buildTicketSummary(insights []TimedTicketInsight) string {
	if len(insights) == 0 {
		return "ticket/list 未返回票档。"
	}

	preBuy := make([]TimedTicketInsight, 0)
	alternate := make([]TimedTicketInsight, 0)
	for _, ticket := range insights {
		if ticket.PreBuySuccessRate != nil {
			preBuy = append(preBuy, ticket)
		}
		if ticket.AlternateSuccessRate != nil {
			alternate = append(alternate, ticket)
		}
	}

	lines := make([]string, 0, 4)
	if len(preBuy) == 0 {
		lines = append(lines, "定时购：接口未返回 preBuySuccessRate，前端不会展示定时购成功率。")
	} else {
		lines = append(lines, fmt.Sprintf("定时购：%d 个票档返回成功率档位，较优票档：%s", len(preBuy), joinTicketBriefs(preBuy, 3, false)))
	}
	if len(alternate) > 0 {
		lines = append(lines, fmt.Sprintf("候补：%d 个票档返回候补成功率，较优票档：%s", len(alternate), joinTicketBriefs(alternate, 3, true)))
	}

	return strings.Join(lines, "\n")
}

func joinTicketBriefs(tickets []TimedTicketInsight, limit int, alternate bool) string {
	if len(tickets) == 0 {
		return "-"
	}
	if len(tickets) < limit {
		limit = len(tickets)
	}
	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		ticket := tickets[i]
		rate := ticket.PreBuySuccessRate
		band := ticket.PreBuyRateBand
		prefix := "定时购"
		if alternate {
			rate = ticket.AlternateSuccessRate
			band = ticket.AlternateRateBand
			prefix = "候补"
		}
		start := ticket.StartTime
		if start == "" {
			start = ticket.TimeText
		}
		brief := fmt.Sprintf("%s/%s/¥%s：%s%s %s", ticket.SessionName, ticket.TicketType, ticket.Price, prefix, formatRate(rate), band)
		if start != "" {
			brief += "，开售 " + start
		}
		parts = append(parts, brief)
	}
	return strings.Join(parts, "；")
}

func timedNotificationSuccessRateText(snapshot *TimedTicketSnapshot) string {
	if snapshot == nil || !visibleRate(snapshot.BestPreBuyRate) {
		return ""
	}
	for _, ticket := range snapshot.Tickets {
		if !visibleRate(ticket.PreBuySuccessRate) || *ticket.PreBuySuccessRate != *snapshot.BestPreBuyRate {
			continue
		}
		parts := []string{formatRate(ticket.PreBuySuccessRate)}
		if ticket.PreBuyRateBand != "" {
			parts = append(parts, ticket.PreBuyRateBand)
		}
		if ticket.TicketType != "" {
			price := ticket.Price
			if price != "" {
				parts = append(parts, fmt.Sprintf("%s ¥%s", ticket.TicketType, price))
			} else {
				parts = append(parts, ticket.TicketType)
			}
		}
		return strings.Join(parts, " / ")
	}
	return formatRate(snapshot.BestPreBuyRate)
}

func visibleRate(rate *float64) bool {
	return rate != nil && *rate >= 0
}

func visibleRatePtr(rate *float64) *float64 {
	if !visibleRate(rate) {
		return nil
	}
	value := *rate
	return &value
}

func sortRate(rate *float64) float64 {
	if !visibleRate(rate) {
		return 1e9
	}
	return *rate
}

func formatRate(rate *float64) string {
	if !visibleRate(rate) {
		return "-"
	}
	return strconv.FormatFloat(*rate, 'f', -1, 64)
}

func rateBandText(rate float64) string {
	switch {
	case rate <= 1:
		return "UI第1档/相对最高"
	case rate <= 2:
		return "UI第2档"
	case rate <= 3:
		return "UI第3档"
	case rate <= 4:
		return "UI第4档"
	case rate <= 5:
		return "UI第5档"
	case rate <= 20:
		return "UI第6档/偏低"
	default:
		return "UI第7档/最低"
	}
}

func saleStatusText(status int) string {
	switch status {
	case 1:
		return "可购买"
	case 2:
		return "即将开售"
	case 3:
		return "已售罄"
	case 9:
		return "候补"
	default:
		return fmt.Sprintf("状态%d", status)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func priceValue(price string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(price), 64)
	if err != nil {
		return 0
	}
	return value
}
