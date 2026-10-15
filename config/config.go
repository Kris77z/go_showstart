package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/viper"
)

type Config struct {
	System    *System     `mapstructure:"system"`
	Showstart *Showstart  `mapstructure:"showstart"` // 单账号模式（向后兼容）
	Accounts  []*Account  `mapstructure:"accounts"`  // 多账号模式
	Ticket    *Ticket     `mapstructure:"ticket"`
	SmtpEmail *smtp_email `mapstructure:"smtp_email"`
	Monitor   *Monitor    `mapstructure:"monitor"`
}

// Account 代表一个抢票账号的完整配置
type Account struct {
	Name      string     `mapstructure:"name"`      // 账号别名，用于日志区分
	Showstart *Showstart `mapstructure:"showstart"` // 账号凭证
	People    []string   `mapstructure:"people"`    // 该账号的观演人列表
}

type System struct {
	MaxGoroutine int `mapstructure:"max_goroutine"`
	MinInterval  int `mapstructure:"min_interval"`
	MaxInterval  int `mapstructure:"max_interval"`
}

type Showstart struct {
	BaseURL          string `mapstructure:"base_url"`
	Sign             string `mapstructure:"sign"`
	Token            string `mapstructure:"token"`
	Cookie           string `mapstructure:"cookie"`
	StFlpv           string `mapstructure:"st_flpv"`
	Cusid            string `mapstructure:"cusid"`
	Cusname          string `mapstructure:"cusname"`
	Cversion         string `mapstructure:"cversion"`
	Cterminal        string `mapstructure:"cterminal"`
	Cdeviceinfo      string `mapstructure:"cdeviceinfo"`
	AuthStateFile    string `mapstructure:"auth_state_file"`
	AuthAccountAlias string `mapstructure:"auth_account_alias"`
}

type Ticket struct {
	ActivityId int          `mapstructure:"activity_id"`
	StartTime  string       `mapstructure:"start_time"`
	List       []TicketList `mapstructure:"list"`
	People     []string     `mapstructure:"people"`
}

type TicketList struct {
	Session string `mapstructure:"session"`
	Price   string `mapstructure:"price"`
}

type smtp_email struct {
	Host     string `mapstructure:"host"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	To       string `mapstructure:"email_to"`
	Enable   bool   `mapstructure:"enable"`
}

type Monitor struct {
	Enable             bool     `mapstructure:"enable"`
	Keywords           []string `mapstructure:"keywords"`
	CityCode           string   `mapstructure:"city_code"`
	IntervalSecond     int      `mapstructure:"interval_seconds"`
	WebhookURL         string   `mapstructure:"webhook_url"`
	StateDir           string   `mapstructure:"state_dir"`
	AlertWebhookURL    string   `mapstructure:"alert_webhook_url"`
	AutoPurchaseEnable bool     `mapstructure:"auto_purchase_enable"`
	AutoPurchasePeople []string `mapstructure:"auto_purchase_people"`
}

func InitCfg() (*Config, error) {
	// 获取当前工作目录
	workDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("未获取到当前工作目录")
	}
	log.Println("当前工作目录：", workDir)

	// 获取可执行文件的路径
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path: %v", err)
	}
	log.Println("可执行文件路径：", exePath)

	// 获取可执行文件的目录
	exeDir := filepath.Dir(exePath)

	// 设置 Viper 的配置文件名和类型
	viper.SetConfigName("config") // 配置文件名（不带扩展名）
	viper.SetConfigType("yaml")   // 配置文件类型

	// 首先尝试从可执行文件目录加载配置
	viper.AddConfigPath(exeDir)

	// 如果在可执行文件目录未找到，则尝试从当前工作目录加载配置
	viper.AddConfigPath(workDir)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil, errors.New("未读取到配置文件，请确认config.yaml是否存在")
		}
	}

	var cfg *Config

	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, errors.New("配置信息映射失败，请检查配置文件格式是否遵循yaml格式")
	}

	err = cfg.Validate()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// 保存手动匹配信息到config.yaml
func SaveCfg(SessionName string, Price string) error {
	filename := "config.yaml"

	// 以文本读取
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}

	// 将文件内容转换为字符串
	content := string(data)

	// 使用正则表达式定位 session:.*\n 的位置 并替换第一个匹配项
	re := regexp.MustCompile(`(?m)session:.*\n`)
	replaced := false
	content = re.ReplaceAllStringFunc(content, func(match string) string {
		if !replaced {
			replaced = true
			return fmt.Sprintf("session: \"%s\"\n", SessionName)
		}
		return match
	})

	// 使用正则表达式定位 price: "" 的位置 并替换第一个匹配项
	re = regexp.MustCompile(`(?m)price:.*\n`)
	replaced = false
	content = re.ReplaceAllStringFunc(content, func(match string) string {
		if !replaced {
			replaced = true
			return fmt.Sprintf("price: \"%s\"\n", Price)
		}
		return match
	})

	// 将修改后的内容写回文件
	err = os.WriteFile(filename, []byte(content), 0644)
	if err != nil {
		return err
	}

	return nil

}

func (cfg *Config) Validate() error {
	monitorEnabled := cfg.Monitor != nil && cfg.Monitor.Enable
	ticketEnabled := cfg.Ticket != nil && len(cfg.Ticket.List) > 0

	if !monitorEnabled && !ticketEnabled {
		return errors.New("配置中未开启监控或抢票功能，请至少启用一项")
	}

	if ticketEnabled {
		if cfg.Ticket == nil {
			return errors.New("未读取到票务配置信息")
		}

		// 检查是否使用多账号模式
		if len(cfg.Accounts) > 0 {
			// 多账号模式：验证每个账号
			for i, acc := range cfg.Accounts {
				if acc.Showstart == nil {
					return fmt.Errorf("账号[%d] %s: 缺少 showstart 配置", i, acc.Name)
				}
				if len(acc.People) == 0 {
					return fmt.Errorf("账号[%d] %s: 未配置观演人信息", i, acc.Name)
				}
				if acc.Showstart.Sign == "" || acc.Showstart.Token == "" {
					return fmt.Errorf("账号[%d] %s: sign 或 token 为空", i, acc.Name)
				}
			}
		} else {
			// 单账号模式：向后兼容
			if len(cfg.Ticket.People) == 0 {
				return errors.New("未读取到观演人信息")
			}
			if cfg.Showstart == nil {
				return errors.New("未读取到 showstart 配置信息")
			}
		}
	}

	if cfg.SmtpEmail == nil {
		if ticketEnabled {
			return errors.New("未读取到邮件配置信息")
		}
	} else {
		if cfg.SmtpEmail.Enable && (cfg.SmtpEmail.Host == "" || cfg.SmtpEmail.Username == "") {
			return errors.New("邮件通知已启用，但 host 或 username 为空")
		}
	}

	if monitorEnabled {
		if len(cfg.Monitor.Keywords) == 0 {
			return errors.New("监控关键词列表为空")
		}
		if cfg.Monitor.IntervalSecond <= 0 {
			cfg.Monitor.IntervalSecond = 180
		}
		if cfg.Monitor.CityCode == "" {
			cfg.Monitor.CityCode = "99999"
		}
		if cfg.Monitor.WebhookURL == "" {
			return errors.New("监控模式需配置 webhook_url")
		}
		// 如果启用自动抢票，验证观演人配置
		if cfg.Monitor.AutoPurchaseEnable {
			if len(cfg.Monitor.AutoPurchasePeople) == 0 {
				return errors.New("启用自动抢票但未配置观演人信息")
			}
			if len(cfg.Monitor.AutoPurchasePeople) > 3 {
				return errors.New("观演人数量不能超过3个")
			}
		}
	}

	return nil
}

// GetEffectiveAccounts 返回有效的账号列表（支持多账号和单账号模式）
func (cfg *Config) GetEffectiveAccounts() []*Account {
	if len(cfg.Accounts) > 0 {
		return cfg.Accounts
	}
	// 单账号模式：将旧配置转换为 Account 格式
	if cfg.Showstart != nil && cfg.Ticket != nil {
		return []*Account{
			{
				Name:      cfg.Showstart.Cusname,
				Showstart: cfg.Showstart,
				People:    cfg.Ticket.People,
			},
		}
	}
	return nil
}
