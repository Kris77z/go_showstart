package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/staparx/go_showstart/config"
)

var ErrDeliveryUnknown = errors.New("delivery_unknown: 提交可能已送达；保留 request_key 并查询，禁止重新提交")

type BridgeBinding struct {
	AccountAlias string `json:"account_alias"`
	Cusid        string `json:"cusid"`
	DeviceNo     string `json:"device_no"`
}

type BridgeSelection struct {
	ActivityID   int      `json:"activity_id"`
	TicketID     string   `json:"ticket_id"`
	Quantity     int      `json:"quantity"`
	UnitPrice    string   `json:"unit_price"`
	TotalAmount  string   `json:"total_amount"`
	PerformerIDs []string `json:"performer_ids"`
	OrderType    int      `json:"order_type"`
	Delivery     string   `json:"delivery"`
	CouponID     string   `json:"coupon_id"`
	AddressID    string   `json:"address_id"`
}

var decimalMoney = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})(\.[0-9]{1,2})?$`)
var requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s BridgeSelection) Validate() error {
	if s.ActivityID < 1 || !identifierPattern.MatchString(s.TicketID) || s.Quantity < 1 || s.Quantity > 100 {
		return errors.New("必须显式指定有效 activity_id、ticket_id 和 quantity")
	}
	if !decimalMoney.MatchString(s.UnitPrice) || !decimalMoney.MatchString(s.TotalAmount) {
		return errors.New("金额须为最多两位小数的十进制字符串")
	}
	unit, _ := new(big.Rat).SetString(s.UnitPrice)
	total, _ := new(big.Rat).SetString(s.TotalAmount)
	if unit.Sign() <= 0 || total.Sign() <= 0 || new(big.Rat).Mul(unit, new(big.Rat).SetInt64(int64(s.Quantity))).Cmp(total) != 0 {
		return errors.New("total_amount 必须等于 unit_price * quantity 且大于零")
	}
	if s.PerformerIDs == nil {
		return errors.New("performer_ids 必须显式提供；无需实名时提供 []")
	}
	seen := map[string]bool{}
	for _, id := range s.PerformerIDs {
		if !identifierPattern.MatchString(id) || seen[id] {
			return errors.New("performer_ids 不能空白或重复")
		}
		seen[id] = true
	}
	if len(s.PerformerIDs) > 0 && len(s.PerformerIDs) != s.Quantity {
		return errors.New("实名人数量必须等于 quantity")
	}
	if s.OrderType != 1 {
		return errors.New("unsupported: 目前仅支持 order_type=1")
	}
	if s.Delivery != "electronic" || s.CouponID != "" || s.AddressID != "" {
		return errors.New("unsupported: 目前仅支持 electronic 配送、不使用优惠券或地址")
	}
	return nil
}

type BridgePrepared struct {
	Version      int             `json:"version"`
	State        string          `json:"state"`
	Binding      BridgeBinding   `json:"binding"`
	Selection    BridgeSelection `json:"selection"`
	Confirmation json.RawMessage `json:"confirmation"`
	Success      *bool           `json:"success,omitempty"`
}

type BridgeOrderState struct {
	Version         int              `json:"version"`
	State           string           `json:"state"`
	RequestKey      string           `json:"request_key"`
	Backend         string           `json:"backend"`
	Binding         BridgeBinding    `json:"binding"`
	OrderID         string           `json:"order_id,omitempty"`
	OrderJobKey     string           `json:"order_job_key,omitempty"`
	CoreOrderKey    string           `json:"core_order_key,omitempty"`
	Reason          string           `json:"reason,omitempty"`
	Dispatch        string           `json:"dispatch,omitempty"`
	Success         *bool            `json:"success,omitempty"`
	Selection       *BridgeSelection `json:"selection,omitempty"`
	StartedAtUnixMS int64            `json:"started_at_unix_ms,omitempty"`
	UpdatedAtUnixMS int64            `json:"updated_at_unix_ms,omitempty"`
}

func (s BridgeOrderState) Unresolved() bool {
	return s.State == "started" || s.State == "delivery_unknown" || s.State == "queued"
}

type BridgeAccountState struct {
	Version               int           `json:"version"`
	State                 string        `json:"state"`
	Binding               BridgeBinding `json:"binding"`
	UnresolvedRequestKeys []string      `json:"unresolved_request_keys"`
}

type BridgeHTTPError struct{ StatusCode int }

func (e *BridgeHTTPError) Error() string { return fmt.Sprintf("bridge HTTP %d", e.StatusCode) }

type BridgeClient struct {
	endpoint string
	binding  BridgeBinding
	http     *http.Client
}

func NewBridgeClient(cfg config.Bridge) (*BridgeClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	u, _ := url.Parse(cfg.Endpoint)
	if u.Hostname() == "localhost" {
		u.Host = net.JoinHostPort("127.0.0.1", u.Port())
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, MaxIdleConnsPerHost: 2}
	return &BridgeClient{endpoint: strings.TrimRight(u.String(), "/"), binding: BridgeBinding{cfg.AccountAlias, cfg.Cusid, cfg.DeviceNo}, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *BridgeClient) Close() { c.http.CloseIdleConnections() }

func (c *BridgeClient) requestBody(ctx context.Context, key string) (map[string]any, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("bridge 请求必须有总 deadline")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !deadline.After(time.Now()) {
		return nil, context.DeadlineExceeded
	}
	if time.Until(deadline) > 60*time.Second {
		return nil, errors.New("bridge 总 deadline 不能超过60s")
	}
	if !requestKeyPattern.MatchString(key) {
		return nil, errors.New("request_key 必须为 8-128 位字母数字及 _-")
	}
	return map[string]any{"account_alias": c.binding.AccountAlias, "cusid": c.binding.Cusid, "device_no": c.binding.DeviceNo, "deadline_unix_ms": deadline.UnixMilli(), "request_key": key}, nil
}

func (c *BridgeClient) post(ctx context.Context, path string, body map[string]any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 1024*1024 {
		return errors.New("bridge 响应过大")
	}
	if err := uniqueResponseKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return errors.New("bridge 响应包含重复或无效JSON字段")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("bridge 响应无效: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return &BridgeHTTPError{res.StatusCode}
	}
	return nil
}

func selectionBody(body map[string]any, s BridgeSelection) {
	body["activity_id"] = s.ActivityID
	body["ticket_id"] = s.TicketID
	body["quantity"] = s.Quantity
	body["unit_price"] = s.UnitPrice
	body["total_amount"] = s.TotalAmount
	body["performer_ids"] = s.PerformerIDs
	body["order_type"] = s.OrderType
	body["delivery"] = s.Delivery
	body["coupon_id"] = s.CouponID
	body["address_id"] = s.AddressID
}

func (c *BridgeClient) Prepare(ctx context.Context, key string, s BridgeSelection) (*BridgePrepared, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	body, err := c.requestBody(ctx, key)
	if err != nil {
		return nil, err
	}
	selectionBody(body, s)
	var out BridgePrepared
	if err = c.post(ctx, "/bridge/v1/orders/prepare", body, &out); err != nil {
		return nil, err
	}
	if out.Version != 1 || out.State != "prepared" || out.Binding != c.binding || len(out.Confirmation) == 0 || string(out.Confirmation) == "null" || (out.Success != nil && !*out.Success) {
		return nil, errors.New("bridge prepare 合同不匹配")
	}
	a, _ := json.Marshal(s)
	b, _ := json.Marshal(out.Selection)
	if !bytes.Equal(a, b) {
		return nil, errors.New("bridge prepare selection 不匹配")
	}
	var confirmation struct {
		ActivityID int    `json:"activity_id"`
		TicketID   string `json:"ticket_id"`
		UnitPrice  string `json:"unit_price"`
		TicketType int    `json:"ticket_type"`
		BuyType    int    `json:"buy_type"`
		Quantity   int    `json:"quantity"`
	}
	if err := json.Unmarshal(out.Confirmation, &confirmation); err != nil || confirmation.ActivityID != s.ActivityID || confirmation.TicketID != s.TicketID || confirmation.UnitPrice != s.UnitPrice || confirmation.TicketType != 1 || confirmation.Quantity != s.Quantity || (confirmation.BuyType != 2 && confirmation.BuyType != 3 && confirmation.BuyType != 4) || (confirmation.BuyType == 2 && len(s.PerformerIDs) != s.Quantity) || (confirmation.BuyType != 2 && len(s.PerformerIDs) != 0) {
		return nil, errors.New("bridge confirmation 合同不匹配")
	}
	return &out, nil
}

func (c *BridgeClient) validateState(out *BridgeOrderState, key string) error {
	if out.Version != 1 || out.RequestKey != key || (out.Backend != "app" && out.Backend != "legacy") || out.Binding != c.binding || (out.Success != nil && !*out.Success) {
		return errors.New("bridge 订单账号或请求合同不匹配")
	}
	switch out.State {
	case "started", "delivery_unknown":
	case "queued":
		if out.OrderJobKey == "" && out.CoreOrderKey == "" {
			return errors.New("bridge queued 缺少持久化查询 key")
		}
	case "created":
		if !validOrderID(out.OrderID) {
			return errors.New("bridge created 缺少 order_id")
		}
	default:
		return errors.New("bridge 订单状态无效")
	}
	for _, id := range []string{out.OrderJobKey, out.CoreOrderKey} {
		if id != "" && !identifierPattern.MatchString(id) {
			return errors.New("bridge query key 无效")
		}
	}
	if out.StartedAtUnixMS < 0 || out.UpdatedAtUnixMS < 0 {
		return errors.New("bridge record timestamp 无效")
	}
	if out.Selection != nil {
		if err := out.Selection.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validOrderID(id string) bool {
	if !identifierPattern.MatchString(id) {
		return false
	}
	switch strings.ToLower(id) {
	case "0", "null", "none", "pending":
		return false
	}
	return true
}

func (c *BridgeClient) Submit(ctx context.Context, key string, s BridgeSelection) (*BridgeOrderState, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	body, err := c.requestBody(ctx, key)
	if err != nil {
		return nil, err
	}
	selectionBody(body, s)
	var out BridgeOrderState
	if err = c.post(ctx, "/bridge/v1/orders", body, &out); err != nil {
		var status *BridgeHTTPError
		if errors.As(err, &status) && status.StatusCode >= 400 && status.StatusCode < 500 && out.Version == 1 && out.State == "rejected" && out.Dispatch == "not_started" && out.RequestKey == key && out.Binding == c.binding {
			return &out, nil
		}
		return &BridgeOrderState{Version: 1, State: "delivery_unknown", RequestKey: key, Backend: "app", Binding: c.binding}, fmt.Errorf("%w: %v", ErrDeliveryUnknown, err)
	}
	if err = c.validateState(&out, key); err != nil {
		return &BridgeOrderState{Version: 1, State: "delivery_unknown", RequestKey: key, Backend: "app", Binding: c.binding}, fmt.Errorf("%w: %v", ErrDeliveryUnknown, err)
	}
	if out.Backend != "app" {
		return &BridgeOrderState{Version: 1, State: "delivery_unknown", RequestKey: key, Backend: "app", Binding: c.binding}, fmt.Errorf("%w: submit backend mismatch", ErrDeliveryUnknown)
	}
	if out.Selection != nil {
		want, _ := json.Marshal(s)
		got, _ := json.Marshal(out.Selection)
		if !bytes.Equal(want, got) {
			return &BridgeOrderState{Version: 1, State: "delivery_unknown", RequestKey: key, Backend: "app", Binding: c.binding}, fmt.Errorf("%w: returned selection mismatch", ErrDeliveryUnknown)
		}
	}
	if out.State == "started" || out.State == "delivery_unknown" {
		return &out, ErrDeliveryUnknown
	}
	return &out, nil
}

func (c *BridgeClient) Status(ctx context.Context, key string, recover bool) (*BridgeOrderState, error) {
	body, err := c.requestBody(ctx, key)
	if err != nil {
		return nil, err
	}
	body["recover"] = recover
	var out BridgeOrderState
	if err = c.post(ctx, "/bridge/v1/orders/status", body, &out); err != nil {
		return nil, err
	}
	if err = c.validateState(&out, key); err != nil {
		return nil, err
	}
	if out.Backend == "legacy" && !out.Unresolved() {
		return nil, errors.New("legacy terminal result requires manual reconciliation")
	}
	return &out, nil
}

func (c *BridgeClient) AccountState(ctx context.Context, key string) (*BridgeAccountState, error) {
	body, err := c.requestBody(ctx, key)
	if err != nil {
		return nil, err
	}
	var out BridgeAccountState
	if err = c.post(ctx, "/bridge/v1/accounts/state", body, &out); err != nil {
		return nil, err
	}
	if out.Version != 1 || out.Binding != c.binding || out.UnresolvedRequestKeys == nil || (out.State != "account_ready" && out.State != "account_blocked") || (out.State == "account_ready") != (len(out.UnresolvedRequestKeys) == 0) {
		return nil, errors.New("bridge account state 合同不匹配")
	}
	for _, key := range out.UnresolvedRequestKeys {
		if !requestKeyPattern.MatchString(key) {
			return nil, errors.New("bridge account state request key 无效")
		}
	}
	return &out, nil
}

// Wait performs one status request at a time. The same total deadline includes
// every request and delay; pending/error never leads back to Submit.
func (c *BridgeClient) Wait(ctx context.Context, key string, interval time.Duration) (*BridgeOrderState, error) {
	if interval <= 0 {
		return nil, errors.New("poll interval 必须大于零")
	}
	var last *BridgeOrderState
	for {
		out, err := c.Status(ctx, key, true)
		if err != nil {
			return last, err
		}
		last = out
		if !out.Unresolved() {
			return out, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func uniqueResponseKeys(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			folded := foldResponseKey(key)
			if !ok || seen[folded] {
				return errors.New("duplicate JSON key")
			}
			seen[folded] = true
			if err = uniqueResponseKeys(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueResponseKeys(d); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	_, err = d.Token()
	return err
}

// encoding/json matches struct fields with Unicode simple folding. Treat the
// same equivalence classes as duplicates before a later field can overwrite an
// earlier unknown or conflicting state.
func foldResponseKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		min := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < min {
				min = next
			}
		}
		b.WriteRune(min)
	}
	return b.String()
}
