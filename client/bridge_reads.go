package client

import (
	"context"
	"encoding/json"
	"errors"
)

// App read responses keep the upstream result object/list. These are separate
// from the legacy WAP structs; in particular coupon/order_list is not orders/list.
type BridgeReadResult struct {
	State   json.RawMessage `json:"state"`
	Success *bool           `json:"success,omitempty"`
	Result  json.RawMessage `json:"result"`
}

func (c *BridgeClient) appRead(ctx context.Context, key, path string, fields map[string]any) (*BridgeReadResult, error) {
	body, err := c.requestBody(ctx, key)
	if err != nil {
		return nil, err
	}
	for k, v := range fields {
		body[k] = v
	}
	var out BridgeReadResult
	if err = c.post(ctx, path, body, &out); err != nil {
		return nil, err
	}
	positive := false
	switch string(out.State) {
	case `1`, `"1"`, `true`, `"success"`, `"SUCCESS"`, `200`, `"200"`:
		positive = true
	}
	if !positive || (out.Success != nil && !*out.Success) || len(out.Result) == 0 || (out.Result[0] != '{' && out.Result[0] != '[') {
		return nil, errors.New("App read response unsuccessful or unsupported")
	}
	return &out, nil
}

func (c *BridgeClient) ReadActivityDetail(ctx context.Context, key string, activityID int) (*BridgeReadResult, error) {
	if activityID < 1 {
		return nil, errors.New("activity_id must be positive")
	}
	return c.appRead(ctx, key, "/bridge/v1/activity/details", map[string]any{"activity_id": activityID})
}

func (c *BridgeClient) ReadPerformers(ctx context.Context, key string, activityID int, ticketID string) (*BridgeReadResult, error) {
	if activityID < 1 || !identifierPattern.MatchString(ticketID) {
		return nil, errors.New("invalid activity_id or ticket_id")
	}
	return c.appRead(ctx, key, "/bridge/v1/performers", map[string]any{"activity_id": activityID, "ticket_id": ticketID})
}

func (c *BridgeClient) ReadOrders(ctx context.Context, key string, pageNo, pageSize int) (*BridgeReadResult, error) {
	if pageNo < 1 || pageSize < 1 || pageSize > 50 {
		return nil, errors.New("invalid order list pagination")
	}
	return c.appRead(ctx, key, "/bridge/v1/orders/list", map[string]any{"page_no": pageNo, "page_size": pageSize})
}

func (c *BridgeClient) ReadOrder(ctx context.Context, key, orderID string) (*BridgeReadResult, error) {
	if !validOrderID(orderID) {
		return nil, errors.New("invalid order_id")
	}
	return c.appRead(ctx, key, "/bridge/v1/orders/detail", map[string]any{"order_id": orderID})
}

func (*BridgeClient) ReadAddresses(context.Context, string) (*BridgeReadResult, error) {
	return nil, errors.New("unsupported: App address mapping is unverified")
}
func (*BridgeClient) ReadCoupons(context.Context, string) (*BridgeReadResult, error) {
	return nil, errors.New("unsupported: coupon mapping is unverified; coupon list is not an order list")
}
