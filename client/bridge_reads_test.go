package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBridgeAppReadsAndExplicitUnsupported(t *testing.T) {
	var calls atomic.Int32
	c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["cusid"] != "123" || body["account_alias"] != "fixture" || body["device_no"] != "device_1" {
			t.Error("read binding missing")
		}
		switch r.URL.Path {
		case "/bridge/v1/activity/details":
			if body["activity_id"] != float64(1) {
				t.Error(body)
			}
		case "/bridge/v1/performers":
			if body["ticket_id"] != "ticket_1" {
				t.Error(body)
			}
		case "/bridge/v1/orders/list":
			if body["page_no"] != float64(1) || body["page_size"] != float64(10) {
				t.Error(body)
			}
		case "/bridge/v1/orders/detail":
			if body["order_id"] != "order_1" {
				t.Error(body)
			}
		default:
			t.Error("unsupported route reached transport", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"state":1,"success":true,"result":{"fixture":true}}`))
	})
	ctx := boundedContext(t, time.Second)
	if _, err := c.ReadActivityDetail(ctx, "request_1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadPerformers(ctx, "request_1", 1, "ticket_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadOrders(ctx, "request_1", 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadOrder(ctx, "request_1", "order_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadAddresses(context.Background(), "request_1"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
	if _, err := c.ReadCoupons(context.Background(), "request_1"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
}

func TestBridgeAppReadsRejectConflictingSuccess(t *testing.T) {
	for _, payload := range []string{`{"state":"1","success":false,"result":[]}`, `{"state":"1","success":"true","result":[]}`, `{"state":"1","result":null}`, `{"success":true,"result":[]}`} {
		c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(payload)) })
		if _, err := c.ReadOrders(boundedContext(t, time.Second), "request_1", 1, 10); err == nil {
			t.Fatalf("accepted %s", payload)
		}
	}
}

func TestBridgeAccountStateRejectsMissingOrConflictingFields(t *testing.T) {
	for _, tc := range []struct {
		state string
		keys  []string
		valid bool
	}{{"account_ready", []string{}, true}, {"account_blocked", []string{"request_1"}, true}, {"", nil, false}, {"account_ready", []string{"request_1"}, false}, {"account_ready", nil, false}, {"account_blocked", []string{}, false}} {
		c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(BridgeAccountState{Version: 1, State: tc.state, Binding: stateFixture().Binding, UnresolvedRequestKeys: tc.keys})
		})
		_, err := c.AccountState(boundedContext(t, time.Second), "request_1")
		if (err == nil) != tc.valid {
			t.Fatalf("state=%s keys=%v err=%v", tc.state, tc.keys, err)
		}
	}
}
