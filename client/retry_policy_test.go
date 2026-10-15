package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/staparx/go_showstart/log"
	"go.uber.org/zap"
)

func TestLegacyCreateBlockedBeforeTransport(t *testing.T) {
	log.Logger = zap.NewNop()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := &ShowStartClient{BashUrl: srv.URL, client: srv.Client(), ClientHeaderConfig: &ClientHeaderConfig{Token: "01234567890123456789012345678901"}}
	_, err := c.Post(context.Background(), "/nj/order/order", `{}`)
	if got := calls.Load(); got != 0 || !errors.Is(err, ErrUnsafeLegacySubmit) {
		t.Fatalf("create POST count = %d, error=%v", got, err)
	}
	if _, err = c.Order(context.Background(), &OrderReq{}); !errors.Is(err, ErrUnsafeLegacySubmit) {
		t.Fatal(err)
	}
	if _, err = c.CoreOrder(context.Background(), "core"); !errors.Is(err, ErrUnsafeLegacySubmit) {
		t.Fatal(err)
	}
}

func TestResultPendingReturnsWithoutRecursing(t *testing.T) {
	log.Logger = zap.NewNop()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"success":true,"result":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"state":"1","success":true,"result":{"orderId":"o1","orderSn":"s1"}}`))
	}))
	defer srv.Close()
	c := &ShowStartClient{BashUrl: srv.URL, client: srv.Client(), ClientHeaderConfig: &ClientHeaderConfig{Token: "01234567890123456789012345678901"}}
	_, err := c.GetOrderResult(context.Background(), "job1")
	if !errors.Is(err, ErrOrderPending) || calls.Load() != 1 {
		t.Fatalf("calls=%d error=%v", calls.Load(), err)
	}
}
