package main

import (
	"context"
	"sync"

	"github.com/staparx/go_showstart/client"
	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"go.uber.org/zap"
	"gopkg.in/gomail.v2"
)

type OrderDetail struct {
	ActivityName, SessionName, Price string
	ActivityID, GoodType             int
	TicketID                         string
}
type AccountOrderResult struct {
	AccountName string
	Order       *OrderDetail
	Success     bool
	Error       error
}
type OrderContext struct {
	AccountName         string
	Client              client.ShowStartIface
	People              []string
	SystemConfig        *config.System
	OrderJobKeyAcquired bool
	OrderJobKeyLock     sync.Mutex
	ResultChan          chan *AccountOrderResult
}

var GlobalResultChan = make(chan *AccountOrderResult, 10)
var channel = make(chan *OrderDetail)
var ErrorChannel = make(chan error)

// These compatibility entry points reject before confirmation, token refresh,
// worker launch or HTTP. The old worker could replay unknown creates.
func ConfirmOrderForAccount(context.Context, *OrderDetail, *config.Account, *config.Config) error {
	return client.ErrUnsafeLegacySubmit
}
func ConfirmOrder(context.Context, *OrderDetail, *config.Config) error {
	return client.ErrUnsafeLegacySubmit
}
func GoOrder(ctx context.Context, index int, c client.ShowStartIface, req *client.OrderReq, cfg *config.Config, order *OrderDetail) {
	if log.Logger != nil {
		log.Logger.Error("旧下单worker已禁用", zap.Error(client.ErrUnsafeLegacySubmit))
	}
	select {
	case ErrorChannel <- client.ErrUnsafeLegacySubmit:
	default:
	}
}
func GoOrderForAccount(ctx context.Context, index int, octx *OrderContext, req *client.OrderReq, order *OrderDetail) {
	if log.Logger != nil {
		log.Logger.Error("旧下单worker已禁用", zap.Error(client.ErrUnsafeLegacySubmit))
	}
	if octx == nil {
		return
	}
	select {
	case octx.ResultChan <- &AccountOrderResult{AccountName: octx.AccountName, Order: order, Error: client.ErrUnsafeLegacySubmit}:
	default:
	}
}

func sendEmail(subject, body string, cfg *config.Config) error {
	m := gomail.NewMessage()
	m.SetHeader("From", cfg.SmtpEmail.Username)
	m.SetHeader("To", cfg.SmtpEmail.To)
	m.SetHeader("Subject", subject)
	m.SetBody("text/plain", body)
	return gomail.NewDialer(cfg.SmtpEmail.Host, 587, cfg.SmtpEmail.Username, cfg.SmtpEmail.Password).DialAndSend(m)
}
