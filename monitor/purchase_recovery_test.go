package monitor

import (
	"context"
	"testing"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
)

func TestLegacyPurchaseRejectsBeforeClient(t *testing.T) {
	// A nil client would panic if any legacy request or worker were started.
	ps := NewPurchaseService(nil, &config.Config{}, nil)
	ps.StartAutoPurchase(context.Background(), 1, "fixture")
	if err := ps.purchaseTicket(context.Background(), 1, "fixture"); err != client.ErrUnsafeLegacySubmit {
		t.Fatal(err)
	}
}
