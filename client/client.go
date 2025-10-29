package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"github.com/staparx/go_showstart/util"
	"github.com/staparx/go_showstart/vars"
	"go.uber.org/zap"
	"strings"
)

type ClientHeaderConfig struct {
	Sign        string `json:"sign"`
	Token       string `json:"token"`
	Cookie      string `json:"cookie"`
	Cdeviceinfo string `json:"cdeviceinfo"`
	Cdeviceno   string `json:"cdeviceno"`
	Crpsign     string `json:"crpsign"`
	Crtraceid   string `json:"crtraceid"`
	Csappid     string `json:"csappid"`
	Cterminal   string `json:"cterminal"`
	Cusat       string `json:"cusat"` //对应accessToken
	Cusid       string `json:"cusid"`
	Cusit       string `json:"cusit"` //对应idToken
	Cusname     string `json:"cusname"`
	Cusut       string `json:"cusut"`
	Cuuserref   string `json:"cuuserref"`
	Cversion    string `json:"cversion"`
	StFlpv      string `json:"st_flpv"`
}

type ShowStartClient struct {
	BashUrl string
	client  *http.Client
	*ClientHeaderConfig
}

func NewShowStartClient(ctx context.Context, cfg *config.Showstart) ShowStartIface {

	c := &ShowStartClient{
		BashUrl: "https://wap.showstart.com/v3",
		client: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 15 * time.Second,
				IdleConnTimeout:       30 * time.Second,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
			},
		},
		ClientHeaderConfig: &ClientHeaderConfig{
			Sign:        cfg.Sign,
			Token:       cfg.Token,
			Cdeviceinfo: cfg.Cdeviceinfo,
			Cdeviceno:   cfg.Token,
			Csappid:     cfg.Cterminal,
			Cterminal:   cfg.Cterminal,
			Cusid:       cfg.Cusid,
			StFlpv:      cfg.StFlpv,
			Cversion:    cfg.Cversion,
			Cuuserref:   cfg.Token,
			Cusname:     cfg.Cusname,
			Cookie:      cfg.Cookie,
			Cusut:       cfg.Sign,
		},
	}

	return c
}

func (c *ShowStartClient) Post(ctx context.Context, path string, body string) ([]byte, error) {
	var lastErr error
	backoff := 500 * time.Millisecond

	for attempt := 0; attempt < 3; attempt++ {
		req, err := c.NewRequest(ctx, "POST", path, body)
		if err != nil {
			return nil, err
		}

		res, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < 2 && shouldRetry(err) {
				log.Logger.Warn("请求失败，将重试", zap.String("path", path), zap.Int("attempt", attempt+1), zap.Error(err))
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return nil, err
		}

		data, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < 2 {
				log.Logger.Warn("读取响应失败，将重试", zap.String("path", path), zap.Int("attempt", attempt+1), zap.Error(readErr))
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return nil, readErr
		}

		if res.StatusCode >= 500 {
			lastErr = fmt.Errorf("http %d", res.StatusCode)
			if attempt < 2 {
				log.Logger.Warn("服务器返回 5xx，即将重试", zap.String("path", path), zap.Int("status", res.StatusCode))
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return nil, fmt.Errorf("http %d: %s", res.StatusCode, string(data))
		}

		if res.StatusCode >= 400 {
			return nil, fmt.Errorf("http %d: %s", res.StatusCode, string(data))
		}

		return data, nil
	}

	return nil, lastErr
}

func shouldRetry(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() || netErr.Temporary() {
			return true
		}
	}
	return false
}

func (c *ShowStartClient) NewRequest(ctx context.Context, method, path string, body string) (*http.Request, error) {
	traceId := util.GenerateTraceId(32)
	if need, ok := vars.EncryptPathMap[path]; ok && need {

		// 加密
		encrypt, err := util.AESEncrypt(body, util.GenerateKey(traceId, c.Token))
		if err != nil {
			return nil, err
		}

		body = fmt.Sprintf(`{"q":"%s"}`, encrypt)
	}

	crpsign := util.GenerateSign(&util.GenerateSignReq{
		Path:      path,
		Data:      body,
		Cusat:     c.Cusat,
		Sign:      c.Sign,
		Cusit:     c.Cusit,
		Cusid:     c.Cusid,
		TraceId:   traceId,
		Token:     c.Token,
		Cterminal: c.Cterminal,
	})

	req, err := http.NewRequestWithContext(ctx, method, c.BashUrl+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Add("cookie", c.Cookie)
	req.Header.Add("cdeviceinfo", c.Cdeviceinfo)
	req.Header.Add("cdeviceno", c.Cdeviceno)
	req.Header.Add("cusut", c.Cusut)
	req.Header.Add("csappid", c.Csappid)
	req.Header.Add("cterminal", c.Cterminal)
	req.Header.Add("cusid", c.Cusid)
	req.Header.Add("cusname", c.Cusname)
	req.Header.Add("cuuserref", c.Cuuserref)
	req.Header.Add("cversion", c.Cversion)
	req.Header.Add("st_flpv", c.StFlpv)
	req.Header.Add("crtraceid", traceId)
	req.Header.Add("crpsign", crpsign)

	if c.Cusat == "" {
		req.Header.Add("cusat", "nil")
	} else {
		req.Header.Add("cusat", c.Cusat)
	}

	if c.Cusit == "" {
		req.Header.Add("cusit", "nil")
	} else {
		req.Header.Add("cusit", c.Cusit)
	}

	return req, nil
}
