package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/staparx/go_showstart/config"
	"github.com/staparx/go_showstart/log"
	"github.com/staparx/go_showstart/util"
	"github.com/staparx/go_showstart/vars"
	"go.uber.org/zap"
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
	BashUrl          string
	client           *http.Client
	authStateFile    string
	authAccountAlias string
	authStateModTime time.Time
	authStateMu      sync.Mutex
	*ClientHeaderConfig
}

type capturedAccount struct {
	Alias       string `json:"alias"`
	Cusat       string `json:"cusat"`
	Cusut       string `json:"cusut"`
	Cusit       string `json:"cusit"`
	Cusid       string `json:"cusid"`
	Cdeviceno   string `json:"cdeviceno"`
	Cuuserref   string `json:"cuuserref"`
	Cdeviceinfo string `json:"cdeviceinfo"`
	Cusname     string `json:"cusname"`
	StFlpv      string `json:"st_flpv"`
	Cookie      string `json:"cookie"`
	CapturedAt  string `json:"captured_at"`
}

func NewShowStartClient(ctx context.Context, cfg *config.Showstart) ShowStartIface {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://wap.showstart.com/v3"
	}

	c := &ShowStartClient{
		BashUrl:          baseURL,
		authStateFile:    cfg.AuthStateFile,
		authAccountAlias: cfg.AuthAccountAlias,
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

func (c *ShowStartClient) refreshAuthFromStateFileIfNeeded() {
	if strings.TrimSpace(c.authStateFile) == "" {
		return
	}

	c.authStateMu.Lock()
	defer c.authStateMu.Unlock()

	path := c.authStateFile
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		log.Logger.Warn("读取浏览器保活凭证文件失败", zap.String("path", path), zap.Error(err))
		return
	}
	if !info.ModTime().After(c.authStateModTime) {
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		log.Logger.Warn("读取浏览器保活凭证文件失败", zap.String("path", path), zap.Error(err))
		return
	}

	var accounts []capturedAccount
	if err := json.Unmarshal(raw, &accounts); err != nil {
		log.Logger.Warn("解析浏览器保活凭证文件失败", zap.String("path", path), zap.Error(err))
		return
	}

	account, ok := selectCapturedAccount(accounts, c.authAccountAlias)
	if !ok {
		log.Logger.Warn("浏览器保活凭证文件里没有可用账号",
			zap.String("path", path),
			zap.String("alias", c.authAccountAlias))
		return
	}

	c.applyCapturedAccount(account)
	c.authStateModTime = info.ModTime()
	log.Logger.Info("已加载浏览器保活凭证",
		zap.String("path", path),
		zap.String("alias", account.Alias),
		zap.String("capturedAt", account.CapturedAt))
}

func selectCapturedAccount(accounts []capturedAccount, alias string) (capturedAccount, bool) {
	alias = strings.TrimSpace(alias)
	if alias != "" {
		for _, account := range accounts {
			if strings.TrimSpace(account.Alias) == alias && account.isUsable() {
				return account, true
			}
		}
		return capturedAccount{}, false
	}

	for _, account := range accounts {
		if account.isUsable() {
			return account, true
		}
	}
	return capturedAccount{}, false
}

func (a capturedAccount) isUsable() bool {
	return strings.TrimSpace(a.Cusut) != "" &&
		strings.TrimSpace(a.Cusid) != "" &&
		strings.TrimSpace(a.Cdeviceno) != "" &&
		strings.TrimSpace(a.StFlpv) != ""
}

func (c *ShowStartClient) applyCapturedAccount(account capturedAccount) {
	if account.Cusut != "" {
		c.Sign = account.Cusut
		c.Cusut = account.Cusut
	}
	if account.Cdeviceno != "" {
		c.Token = account.Cdeviceno
		c.Cdeviceno = account.Cdeviceno
		c.Cuuserref = account.Cdeviceno
	}
	if account.Cuuserref != "" {
		c.Cuuserref = account.Cuuserref
	}
	if account.Cookie != "" {
		c.Cookie = account.Cookie
	}
	if account.Cusat != "" {
		c.Cusat = account.Cusat
	}
	if account.Cusit != "" {
		c.Cusit = account.Cusit
	}
	if account.Cusid != "" {
		c.Cusid = account.Cusid
	}
	if account.Cusname != "" {
		c.Cusname = account.Cusname
	}
	if account.Cdeviceinfo != "" {
		c.Cdeviceinfo = account.Cdeviceinfo
	}
	if account.StFlpv != "" {
		c.StFlpv = account.StFlpv
	}
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
	c.refreshAuthFromStateFileIfNeeded()

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
