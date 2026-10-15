package monitor

import (
	"strings"
	"testing"
	"time"

	"github.com/staparx/go_showstart/client"
)

func TestBuildTimedTicketSnapshotUsesPreBuyRate(t *testing.T) {
	goodRate := 1.0
	weakRate := 21.0
	hiddenRate := -1.0

	resp := &client.ActivityTicketListResp{
		Result: []*client.ActivityTicket{
			{
				SessionName: "2026-07-18 周六 19:30",
				SessionID:   3501260,
				TicketPriceList: []struct {
					Price      string               `json:"price"`
					TicketList []*client.TicketInfo `json:"ticketList"`
				}{
					{
						Price: "388",
						TicketList: []*client.TicketInfo{
							{
								TicketID:          "vip",
								TicketType:        "VIP票",
								SellingPrice:      "388",
								SaleStatus:        1,
								StartTime:         1784374200000,
								PreBuySuccessRate: &weakRate,
							},
						},
					},
					{
						Price: "288",
						TicketList: []*client.TicketInfo{
							{
								TicketID:          "normal",
								TicketType:        "全价票",
								SellingPrice:      "288",
								SaleStatus:        1,
								StartTime:         1784374200000,
								PreBuySuccessRate: &goodRate,
							},
							{
								TicketID:          "hidden",
								TicketType:        "隐藏票档",
								SellingPrice:      "188",
								SaleStatus:        1,
								PreBuySuccessRate: &hiddenRate,
							},
						},
					},
				},
			},
		},
	}

	snapshot := buildTimedTicketSnapshot("301047", resp, time.FixedZone("CST", 8*3600))
	if len(snapshot.Tickets) != 3 {
		t.Fatalf("expected 3 tickets, got %d", len(snapshot.Tickets))
	}
	if snapshot.Tickets[0].TicketID != "normal" {
		t.Fatalf("expected lower preBuy rate first, got %s", snapshot.Tickets[0].TicketID)
	}
	if snapshot.Tickets[0].PreBuyRateBand != "UI第1档/相对最高" {
		t.Fatalf("unexpected band: %s", snapshot.Tickets[0].PreBuyRateBand)
	}
	if snapshot.Tickets[2].PreBuySuccessRate != nil {
		t.Fatalf("negative preBuy rate should be hidden")
	}
	if snapshot.BestPreBuyRate == nil || *snapshot.BestPreBuyRate != goodRate {
		t.Fatalf("unexpected best preBuy rate: %v", snapshot.BestPreBuyRate)
	}
	if !strings.Contains(snapshot.Summary, "定时购：2 个票档返回成功率档位") {
		t.Fatalf("unexpected summary: %s", snapshot.Summary)
	}
	if got := timedNotificationSuccessRateText(snapshot); got != "1 / UI第1档/相对最高 / 全价票 ¥288" {
		t.Fatalf("unexpected notification rate text: %s", got)
	}
}

func TestTimedNotificationSuccessRateTextEmptyWithoutPreBuyRate(t *testing.T) {
	hiddenRate := -1.0
	resp := &client.ActivityTicketListResp{
		Result: []*client.ActivityTicket{
			{
				SessionName: "2026-07-18 周六 19:30",
				SessionID:   3501260,
				TicketPriceList: []struct {
					Price      string               `json:"price"`
					TicketList []*client.TicketInfo `json:"ticketList"`
				}{
					{
						Price: "388",
						TicketList: []*client.TicketInfo{
							{
								TicketID:          "vip",
								TicketType:        "VIP票",
								SellingPrice:      "388",
								SaleStatus:        1,
								PreBuySuccessRate: &hiddenRate,
							},
						},
					},
				},
			},
		},
	}

	snapshot := buildTimedTicketSnapshot("301047", resp, time.FixedZone("CST", 8*3600))
	if snapshot.BestPreBuyRate != nil {
		t.Fatalf("expected no best preBuy rate, got %v", *snapshot.BestPreBuyRate)
	}
	if got := timedNotificationSuccessRateText(snapshot); got != "" {
		t.Fatalf("expected empty notification rate text, got %s", got)
	}
}
