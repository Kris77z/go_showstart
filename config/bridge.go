package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Bridge is an explicit, separate selection. It does not load legacy secrets or
// change the monitor's Showstart.BaseURL.
type Bridge struct {
	Endpoint     string
	AccountAlias string
	Cusid        string
	DeviceNo     string
}

func (b Bridge) Validate() error {
	u, err := url.Parse(b.Endpoint)
	if err != nil {
		return errors.New("bridge endpoint 无效")
	}
	if u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("bridge endpoint 必须是无凭据、query和路径的本机 http 地址")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("bridge endpoint 只允许 loopback")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("bridge endpoint 必须显式指定有效端口")
	}
	for name, value := range map[string]string{"account_alias": b.AccountAlias, "cusid": b.Cusid, "device_no": b.DeviceNo} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s 必须显式指定且有效", name)
		}
	}
	return nil
}
