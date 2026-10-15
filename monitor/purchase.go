package monitor

import (
	"context"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"go.uber.org/zap"
)

// Kept for source compatibility only. Legacy automatic selection and its
// worker loops cannot enter the new journal-backed write path.
type PurchaseService struct{}

func NewPurchaseService(client.ShowStartIface, *config.Config, *Notifier) *PurchaseService {
	return &PurchaseService{}
}
func (*PurchaseService) StartAutoPurchase(context.Context, int, string) {
	if log.Logger != nil {
		log.Logger.Error("自动下单入口已禁用", zap.Error(client.ErrUnsafeLegacySubmit))
	}
}
func (*PurchaseService) purchaseTicket(context.Context, int, string) error {
	return client.ErrUnsafeLegacySubmit
}
