package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/staparx/go_showstart/client"
)

func TestCLIRequiresExplicitConfirmationAndSelection(t *testing.T) {
	for _, args := range [][]string{{"submit"}, {"prepare"}, {"recover", "--wait", "--timeout", "61s"}, {"status", "--wait"}, {"submit", "--confirm", "--performer-ids", "null"}} {
		var out, errout bytes.Buffer
		if code := run(context.Background(), args, &out, &errout); code == 0 || strings.TrimSpace(errout.String()) == "" || out.Len() != 0 {
			t.Fatalf("args=%v code=%d out=%s err=%s", args, code, out.String(), errout.String())
		}
	}
}

func TestHelpReturnsSuccessWithoutBusinessRequest(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"prepare", "--help"}, {"submit", "-h"}, {"status", "--help"}, {"recover", "-h"}} {
		var out, errout bytes.Buffer
		if code := run(context.Background(), args, &out, &errout); code != 0 || out.Len()+errout.Len() == 0 {
			t.Fatalf("args=%v code=%d out=%s err=%s", args, code, out.String(), errout.String())
		}
	}
}

func TestSubmitExitReflectsBusinessState(t *testing.T) {
	for _, state := range []string{"rejected", "delivery_unknown", "started", "created", "queued"} {
		t.Run(state, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result := client.BridgeOrderState{Version: 1, State: state, RequestKey: "request_1", Backend: "app", Binding: client.BridgeBinding{AccountAlias: "fixture", Cusid: "123", DeviceNo: "device"}}
				if state == "rejected" {
					w.WriteHeader(409)
					result.Dispatch = "not_started"
				}
				if state == "created" {
					result.OrderID = "order_1"
				}
				if state == "queued" {
					result.OrderJobKey = "job_1"
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer srv.Close()
			args := []string{"submit", "--confirm", "--endpoint", srv.URL, "--account-alias", "fixture", "--cusid", "123", "--device-no", "device", "--request-key", "request_1", "--activity-id", "1", "--ticket-id", "ticket_1", "--quantity", "1", "--unit-price", "19.90", "--total-amount", "19.90", "--performer-ids", "[]", "--order-type", "1", "--delivery", "electronic"}
			var out, errout bytes.Buffer
			code := run(context.Background(), args, &out, &errout)
			if !strings.Contains(out.String(), `"state":"`+state+`"`) {
				t.Fatalf("missing structured state: %s", out.String())
			}
			if (code == 0) != (state == "created" || state == "queued") {
				t.Fatalf("state=%s code=%d err=%s", state, code, errout.String())
			}
		})
	}
}
