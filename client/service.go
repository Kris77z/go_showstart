package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrUnsafeLegacySubmit = errors.New("旧 Go 写入口已禁用；请用显式 bridge-order CLI 和持久化订单记录提交")
var ErrOrderPending = errors.New("订单 pending；结果未决，不能重新创建")

type ShowStartIface interface {
	GetToken(context.Context) error
	ActivitySearchList(context.Context, string, string) (*ActivitySearchListResp, error)
	ActivityDetail(context.Context, int) (*ActivityDetailResp, error)
	ActivityTicketList(context.Context, int) (*ActivityTicketListResp, error)
	Confirm(context.Context, int, string, string) (*ConfirmResp, error)
	AdressList(context.Context) (*AdressListResp, error)
	CpList(context.Context, string) (*CpListResp, error)
	OrderList(context.Context, *OrderListReq) (*OrderListResp, error)
	Order(context.Context, *OrderReq) (*OrderResp, error)
	CoreOrder(context.Context, string) (*OrderCoreResp, error)
	GetOrderResult(context.Context, string) (*GetOrderResultResp, error)
}

func (c *ShowStartClient) GetToken(ctx context.Context) error {
	c.tokenRefreshMu.Lock()
	defer c.tokenRefreshMu.Unlock()
	c.refreshAuthFromStateFileIfNeeded()
	c.authStateMu.Lock()
	auth := *c.ClientHeaderConfig
	c.authStateMu.Unlock()
	raw, err := c.postWithAuth(ctx, "/waf/gettoken", `{"trackPath":""}`, &auth)
	if err != nil {
		return err
	}
	var resp GetTokenResp
	if err = json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.ShowStartCommonResp == nil || (!resp.Success && resp.State != "1") || resp.Result.AccessToken.AccessToken == "" || resp.Result.IDToken.IDToken == "" {
		return errors.New("token response invalid or rejected")
	}
	c.authStateMu.Lock()
	defer c.authStateMu.Unlock()
	if *c.ClientHeaderConfig != auth {
		return errors.New("credentials changed during token refresh; discard stale response")
	}
	c.Cusat = resp.Result.AccessToken.AccessToken
	c.Cusit = resp.Result.IDToken.IDToken
	return nil
}

// Reads may refresh once after a confirmed auth refusal. They never recurse.
func (c *ShowStartClient) read(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := c.Post(ctx, path, string(raw))
		if err != nil {
			return err
		}
		var common ShowStartCommonResp
		if err = json.Unmarshal(result, &common); err != nil {
			return err
		}
		if common.State == "token-expire-at" || common.Msg == "登录过期了，请重新登录！" {
			if attempt == 1 {
				return errors.New("authentication rejected after one refresh")
			}
			if err = c.GetToken(ctx); err != nil {
				return err
			}
			continue
		}
		if !common.Success && common.State != "1" {
			return fmt.Errorf("business rejected: %s", common.Msg)
		}
		return json.Unmarshal(result, out)
	}
	return errors.New("authentication rejected")
}

func (c *ShowStartClient) ActivitySearchList(ctx context.Context, cityCode, keyword string) (*ActivitySearchListResp, error) {
	var out ActivitySearchListResp
	err := c.read(ctx, "/wap/activity/list", map[string]any{"pageNo": 1, "cityCode": cityCode, "keyword": keyword, "style": "", "activityIds": "", "couponCode": "", "performerId": "", "hosterId": "", "siteId": "", "tag": "", "tourId": "", "themeId": "", "trackPath": ""}, &out)
	return &out, err
}

func (c *ShowStartClient) ActivityDetail(ctx context.Context, id int) (*ActivityDetailResp, error) {
	var out ActivityDetailResp
	err := c.read(ctx, "/wap/activity/details", map[string]any{"activityId": fmt.Sprint(id), "coupon": "", "shareId": "", "trackPath": ""}, &out)
	return &out, err
}

func (c *ShowStartClient) ActivityTicketList(ctx context.Context, id int) (*ActivityTicketListResp, error) {
	var out ActivityTicketListResp
	err := c.read(ctx, "/wap/activity/V2/ticket/list", map[string]any{"activityId": fmt.Sprint(id), "coupon": "", "trackPath": ""}, &out)
	return &out, err
}

func (c *ShowStartClient) Confirm(ctx context.Context, id int, ticketID, num string) (*ConfirmResp, error) {
	var out ConfirmResp
	err := c.read(ctx, "/order/wap/order/confirm", map[string]any{"sequence": fmt.Sprint(id), "ticketId": ticketID, "ticketNum": num, "trackPath": ""}, &out)
	return &out, err
}

func (c *ShowStartClient) AdressList(ctx context.Context) (*AdressListResp, error) {
	var out AdressListResp
	err := c.read(ctx, "/wap/address/list", map[string]any{"trackPath": ""}, &out)
	return &out, err
}

func (c *ShowStartClient) CpList(ctx context.Context, ticketID string) (*CpListResp, error) {
	var out CpListResp
	err := c.read(ctx, "/wap/cp/list", map[string]any{"ticketPriceId": ticketID, "audienceWhitelistPolicy": 0, "trackPath": ""}, &out)
	return &out, err
}

func (c *ShowStartClient) OrderList(ctx context.Context, req *OrderListReq) (*OrderListResp, error) {
	var out OrderListResp
	err := c.read(ctx, "/nj/coupon/order_list", req, &out)
	return &out, err
}

func (*ShowStartClient) Order(context.Context, *OrderReq) (*OrderResp, error) {
	return nil, ErrUnsafeLegacySubmit
}
func (*ShowStartClient) CoreOrder(context.Context, string) (*OrderCoreResp, error) {
	return nil, ErrUnsafeLegacySubmit
}

// A single read returns pending to its owner. There is no recursive wait or
// authentication retry that could accumulate goroutines.
func (c *ShowStartClient) GetOrderResult(ctx context.Context, key string) (*GetOrderResultResp, error) {
	if key == "" {
		return nil, errors.New("orderJobKey cannot be empty")
	}
	raw, _ := json.Marshal(map[string]string{"orderJobKey": key, "trackPath": ""})
	data, err := c.Post(ctx, "/nj/order/getOrderResult", string(raw))
	if err != nil {
		return nil, err
	}
	var envelope struct {
		ShowStartCommonResp
		Result json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if string(envelope.Result) == `"pending"` {
		return nil, ErrOrderPending
	}
	if !envelope.Success && envelope.State != "1" {
		return nil, errors.New("order result unresolved or rejected")
	}
	var out GetOrderResultResp
	if err = json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out.Result.OrderID == "" && out.Result.OrderSn == "" {
		return nil, errors.New("order result lacks order ID")
	}
	return &out, nil
}
