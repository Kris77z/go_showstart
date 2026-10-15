package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"go.uber.org/zap"
)

type competingClient struct {
	client.ShowStartIface
	calls   atomic.Int32
	barrier chan struct{}
	once    sync.Once
}

func (c *competingClient) Order(ctx context.Context, req *client.OrderReq) (*client.OrderResp, error) {
	if c.calls.Add(1) == 2 {
		c.once.Do(func() { close(c.barrier) })
	}
	select {
	case <-c.barrier:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	r := &client.OrderResp{}
	r.Result.OrderJobKey = "job"
	return r, nil
}

func (c *competingClient) GetOrderResult(ctx context.Context, key string) (*client.GetOrderResultResp, error) {
	r := &client.GetOrderResultResp{}
	r.Result.OrderSn = "order"
	return r, nil
}

func TestLegacyWorkersRejectBeforeCreating(t *testing.T) {
	log.Logger = zap.NewNop()
	c := &competingClient{barrier: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	octx := &OrderContext{Client: c, SystemConfig: &config.System{}, ResultChan: make(chan *AccountOrderResult, 2)}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); GoOrderForAccount(ctx, i, octx, &client.OrderReq{}, &OrderDetail{}) }(i)
	}
	wg.Wait()
	if c.calls.Load() != 0 {
		t.Fatalf("creates=%d, want 0", c.calls.Load())
	}
	if err := ConfirmOrderForAccount(ctx, &OrderDetail{}, &config.Account{}, &config.Config{}); err != client.ErrUnsafeLegacySubmit {
		t.Fatal(err)
	}
}
