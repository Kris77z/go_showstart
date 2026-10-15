package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"github.com/staparx/go_showstart/util"
	"go.uber.org/zap"
)

func selectionFixture() BridgeSelection {
	return BridgeSelection{ActivityID: 1, TicketID: "ticket_1", Quantity: 2, UnitPrice: "19.90", TotalAmount: "39.80", PerformerIDs: []string{"person_1", "person_2"}, OrderType: 1, Delivery: "electronic"}
}
func bridgeFixture(t *testing.T, h http.HandlerFunc) (*BridgeClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	c, err := NewBridgeClient(config.Bridge{Endpoint: srv.URL, AccountAlias: "fixture", Cusid: "123", DeviceNo: "device_1"})
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); srv.Close() })
	return c, srv
}
func boundedContext(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
func stateFixture() BridgeOrderState {
	return BridgeOrderState{Version: 1, State: "created", Backend: "app", RequestKey: "request_1", Binding: BridgeBinding{"fixture", "123", "device_1"}, OrderID: "order_1"}
}

func TestBridgePrepareExplicitSelection(t *testing.T) {
	selection := selectionFixture()
	c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bridge/v1/orders/prepare" {
			t.Error(r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, key := range []string{"account_alias", "cusid", "device_no", "deadline_unix_ms", "request_key", "activity_id", "ticket_id", "quantity", "unit_price", "total_amount", "performer_ids", "order_type", "delivery", "coupon_id", "address_id"} {
			if _, ok := body[key]; !ok {
				t.Errorf("missing %s", key)
			}
		}
		for _, key := range []string{"sign", "token", "cusut", "cusat", "cusit"} {
			if _, ok := body[key]; ok {
				t.Errorf("secret field %s", key)
			}
		}
		_ = json.NewEncoder(w).Encode(BridgePrepared{Version: 1, State: "prepared", Binding: stateFixture().Binding, Selection: selection, Confirmation: json.RawMessage(`{"activity_id":1,"ticket_id":"ticket_1","unit_price":"19.90","ticket_type":1,"buy_type":2,"quantity":2}`)})
	})
	if _, err := c.Prepare(boundedContext(t, time.Second), "request_1", selection); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeSubmitNeverRetriesUnconfirmedResponses(t *testing.T) {
	for _, scenario := range []string{"500", "400", "redirect", "malformed", "duplicate", "case_duplicate", "unicode_case_duplicate", "success_case_duplicate", "binding_case_duplicate", "wrong_binding", "wrong_key", "wrong_selection", "foreign_backend", "unproved_rejection", "conflicting_success", "invalid_order_id", "response_lost"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch scenario {
				case "500":
					w.WriteHeader(500)
					_, _ = w.Write([]byte(`{}`))
				case "400":
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"state":"rejected"}`))
				case "redirect":
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(307)
				case "malformed":
					_, _ = w.Write([]byte(`{"state":`))
				case "duplicate":
					_, _ = w.Write([]byte(`{"version":1,"state":"delivery_unknown","state":"created","backend":"app","request_key":"request_1","binding":{"account_alias":"fixture","cusid":"123","device_no":"device_1"},"order_id":"order_1"}`))
				case "case_duplicate":
					_, _ = w.Write([]byte(`{"version":1,"state":"delivery_unknown","State":"created","backend":"app","request_key":"request_1","binding":{"account_alias":"fixture","cusid":"123","device_no":"device_1"},"order_id":"order_1"}`))
				case "unicode_case_duplicate":
					_, _ = w.Write([]byte(`{"version":1,"state":"delivery_unknown","ſtate":"created","backend":"app","request_key":"request_1","binding":{"account_alias":"fixture","cusid":"123","device_no":"device_1"},"order_id":"order_1"}`))
				case "success_case_duplicate":
					_, _ = w.Write([]byte(`{"version":1,"state":"created","success":false,"SUCCESS":true,"backend":"app","request_key":"request_1","binding":{"account_alias":"fixture","cusid":"123","device_no":"device_1"},"order_id":"order_1"}`))
				case "binding_case_duplicate":
					_, _ = w.Write([]byte(`{"version":1,"state":"created","backend":"app","request_key":"request_1","binding":{"account_alias":"fixture","cusid":"other","CUSID":"123","device_no":"device_1"},"order_id":"order_1"}`))
				case "response_lost":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				default:
					state := stateFixture()
					if scenario == "wrong_binding" {
						state.Binding.Cusid = "other"
					}
					if scenario == "wrong_key" {
						state.RequestKey = "request_other"
					}
					if scenario == "unproved_rejection" {
						state.State = "rejected"
					}
					if scenario == "foreign_backend" {
						state.Backend = "legacy"
					}
					if scenario == "wrong_selection" {
						selection := selectionFixture()
						selection.TicketID = "other"
						state.Selection = &selection
					}
					if scenario == "conflicting_success" {
						v := false
						state.Success = &v
					}
					if scenario == "invalid_order_id" {
						state.OrderID = "pending"
					}
					_ = json.NewEncoder(w).Encode(state)
				}
			})
			state, err := c.Submit(boundedContext(t, time.Second), "request_1", selectionFixture())
			if !errors.Is(err, ErrDeliveryUnknown) || state == nil || state.State != "delivery_unknown" || calls.Load() != 1 {
				t.Fatalf("state=%+v err=%v creates=%d", state, err, calls.Load())
			}
		})
	}
}

func TestLegacyRecoveryShowsUnresolvedRecordForManualReconciliation(t *testing.T) {
	for _, state := range []string{"started", "delivery_unknown", "queued", "created"} {
		t.Run(state, func(t *testing.T) {
			c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/bridge/v1/orders/status" {
					t.Error("legacy recovery must never call create or App query route")
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if _, ok := body["order_job_key"]; ok {
					t.Error("invented query key")
				}
				out := stateFixture()
				out.Backend = "legacy"
				out.State = state
				out.Reason = "legacy_requires_manual_reconciliation"
				out.StartedAtUnixMS = 1
				out.UpdatedAtUnixMS = 2
				if state == "queued" {
					out.OrderJobKey = "job_1"
				}
				_ = json.NewEncoder(w).Encode(out)
			})
			out, err := c.Status(boundedContext(t, time.Second), "request_1", true)
			if state == "created" {
				if err == nil {
					t.Fatal("legacy terminal treated as App success")
				}
				return
			}
			if err != nil || out.Backend != "legacy" || out.State != state || out.Reason != "legacy_requires_manual_reconciliation" || out.StartedAtUnixMS != 1 {
				t.Fatalf("state=%+v err=%v", out, err)
			}
		})
	}
}

func TestBridgeExplicitPreDispatchRefusal(t *testing.T) {
	c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(BridgeOrderState{Version: 1, State: "rejected", Dispatch: "not_started", RequestKey: "request_1", Binding: stateFixture().Binding, Reason: "account_blocked"})
	})
	state, err := c.Submit(boundedContext(t, time.Second), "request_1", selectionFixture())
	if err != nil || state.State != "rejected" || state.Dispatch != "not_started" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestBridgeSDKSubmitReturnsUnknownErrorForUnresolvedState(t *testing.T) {
	for _, state := range []string{"started", "delivery_unknown"} {
		t.Run(state, func(t *testing.T) {
			c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				out := stateFixture()
				out.State = state
				out.OrderID = ""
				_ = json.NewEncoder(w).Encode(out)
			})
			out, err := c.Submit(boundedContext(t, time.Second), "request_1", selectionFixture())
			if out == nil || out.State != state || !errors.Is(err, ErrDeliveryUnknown) {
				t.Fatalf("state=%+v err=%v", out, err)
			}
		})
	}
}

func TestBridgeSubmitRequiresBudgetAndValidSelectionBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	if _, err := c.Submit(context.Background(), "request_1", selectionFixture()); err == nil {
		t.Fatal("accepted no deadline")
	}
	ctx, cancel := context.WithCancel(boundedContext(t, time.Second))
	cancel()
	if _, err := c.Submit(ctx, "request_1", selectionFixture()); err == nil {
		t.Fatal("accepted cancelled context")
	}
	s := selectionFixture()
	s.PerformerIDs = nil
	if _, err := c.Submit(boundedContext(t, time.Second), "request_1", s); err == nil {
		t.Fatal("accepted implicit performers")
	}
	if calls.Load() != 0 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestBridgeRecoveryPollsSeriallyWithOneDeadline(t *testing.T) {
	var active, maxActive, calls atomic.Int32
	var deadline atomic.Int64
	c, _ := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bridge/v1/orders/status" {
			t.Error("recovery attempted create")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["recover"] != true || body["request_key"] != "request_1" {
			t.Error(body)
		}
		if _, ok := body["order_job_key"]; ok {
			t.Error("client must not invent a query key")
		}
		ms := int64(body["deadline_unix_ms"].(float64))
		if old := deadline.Load(); old != 0 && old != ms {
			t.Error("deadline renewed")
		}
		deadline.Store(ms)
		n := active.Add(1)
		defer active.Add(-1)
		if n > maxActive.Load() {
			maxActive.Store(n)
		}
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		state := stateFixture()
		state.State = "queued"
		state.OrderID = ""
		state.OrderJobKey = "job_1"
		_ = json.NewEncoder(w).Encode(state)
	})
	state, err := c.Wait(boundedContext(t, 85*time.Millisecond), "request_1", 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || state == nil || !state.Unresolved() {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	count := calls.Load()
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != count || maxActive.Load() != 1 || count < 2 {
		t.Fatalf("calls=%d later=%d maxactive=%d", count, calls.Load(), maxActive.Load())
	}
}

func TestBridgeSelectionBoundaries(t *testing.T) {
	for _, name := range []string{"ticket_chars", "ticket_length", "quantity", "money_size", "precision", "total", "performer_chars", "performer_duplicate", "performer_count", "coupon", "address", "delivery", "type"} {
		t.Run(name, func(t *testing.T) {
			s := selectionFixture()
			switch name {
			case "ticket_chars":
				s.TicketID = "a.b"
			case "ticket_length":
				s.TicketID = strings.Repeat("t", 129)
			case "quantity":
				s.Quantity = 101
				s.TotalAmount = "2009.90"
			case "money_size":
				s.UnitPrice = "1000000000"
				s.TotalAmount = "2000000000"
			case "precision":
				s.UnitPrice = "19.900"
			case "total":
				s.TotalAmount = "39.79"
			case "performer_chars":
				s.PerformerIDs = []string{"a:b", "p2"}
			case "performer_duplicate":
				s.PerformerIDs = []string{"p1", "p1"}
			case "performer_count":
				s.PerformerIDs = []string{"p1"}
			case "coupon":
				s.CouponID = "c"
			case "address":
				s.AddressID = "a"
			case "delivery":
				s.Delivery = "paper"
			case "type":
				s.OrderType = 2
			}
			if s.Validate() == nil {
				t.Fatalf("accepted %+v", s)
			}
		})
	}
	s := selectionFixture()
	s.PerformerIDs = []string{}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyMonitorThreeReadsKeepContract(t *testing.T) {
	log.Logger = zap.NewNop()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/waf/gettoken":
			_, _ = w.Write([]byte(`{"success":true,"result":{"accessToken":{"access_token":"at"},"idToken":{"id_token":"it"}}}`))
		case "/wap/activity/list":
			_, _ = w.Write([]byte(`{"state":"1","result":{"activityInfo":[{"activityId":1,"title":"fixture"}]}}`))
		case "/wap/activity/V2/ticket/list":
			_, _ = w.Write([]byte(`{"state":"1","result":[{"sessionName":"fixture","ticketList":[{"ticketId":"ticket_1","sellingPrice":"19.90"}]}]}`))
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := NewShowStartClient(context.Background(), &config.Showstart{BaseURL: srv.URL})
	ctx := boundedContext(t, time.Second)
	if err := c.GetToken(ctx); err != nil {
		t.Fatal(err)
	}
	if r, err := c.ActivitySearchList(ctx, "99999", "fixture"); err != nil || len(r.Result.ActivityInfo) != 1 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if r, err := c.ActivityTicketList(ctx, 1); err != nil || len(r.Result) != 1 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if calls.Load() != 3 {
		t.Fatal(calls.Load())
	}
}

func TestLegacyRequestUsesOneCredentialSnapshotDuringRefresh(t *testing.T) {
	c := NewShowStartClient(context.Background(), &config.Showstart{}).(*ShowStartClient)
	apply := func(s string) {
		c.authStateMu.Lock()
		defer c.authStateMu.Unlock()
		c.applyCapturedAccount(capturedAccount{Cusut: s, Cusat: s, Cusit: s, Cusid: s, Cdeviceno: s, StFlpv: s})
	}
	apply("initial")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			apply(strings.Repeat(string(rune('a'+i%20)), 32))
		}
	}()
	for j := 0; j < 6; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				r, err := c.NewRequest(context.Background(), "POST", "/wap/activity/list", `{"sign":"stale","st_flpv":"stale"}`)
				if err != nil {
					t.Error(err)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				_ = r.Body.Close()
				var body map[string]string
				_ = json.Unmarshal(raw, &body)
				if body["sign"] != r.Header.Get("cusut") || body["st_flpv"] != r.Header.Get("st_flpv") || r.Header.Get("cusid") != r.Header.Get("cusat") {
					t.Error("mixed credential snapshot")
				}
				expected := util.GenerateSign(&util.GenerateSignReq{Path: "/wap/activity/list", Data: string(raw), Cusat: r.Header.Get("cusat"), Sign: body["sign"], Cusit: r.Header.Get("cusit"), Cusid: r.Header.Get("cusid"), TraceId: r.Header.Get("crtraceid"), Token: r.Header.Get("cdeviceno"), Cterminal: r.Header.Get("cterminal")})
				if expected != r.Header.Get("crpsign") {
					t.Error("signature snapshot mismatch")
				}
			}
		}()
	}
	wg.Wait()
}

func TestTokenRefreshDiscardsResponseForChangedCredentials(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"success":true,"result":{"accessToken":{"access_token":"stale"},"idToken":{"id_token":"stale"}}}`))
	}))
	defer srv.Close()
	c := NewShowStartClient(context.Background(), &config.Showstart{BaseURL: srv.URL}).(*ShowStartClient)
	done := make(chan error, 1)
	go func() { done <- c.GetToken(boundedContext(t, time.Second)) }()
	<-entered
	c.authStateMu.Lock()
	c.applyCapturedAccount(capturedAccount{Cusat: "new", Cusit: "new", Cusid: "new", Cusut: "new", Cdeviceno: "new", StFlpv: "new"})
	c.authStateMu.Unlock()
	close(release)
	if err := <-done; err == nil {
		t.Fatal("accepted stale token response")
	}
	c.authStateMu.Lock()
	defer c.authStateMu.Unlock()
	if c.Cusat != "new" || c.Cusit != "new" {
		t.Fatal("stale response corrupted credentials")
	}
}
