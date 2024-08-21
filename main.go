package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/urfave/cli/v2"
	"golang.org/x/net/proxy"
	"gopkg.in/telebot.v3"
	"gopkg.in/yaml.v3"
)

var (
	cfg *config
)

const (
	TELEGRAM_API  = "https://api.telegram.org"
	CONFIG_FILE   = "config.yaml"
	ADMIN_USER_ID = -1
	DATA_DIR      = "pics"
	MAX_THREADS   = 10
)

type config struct {
	ProxyString string `yaml:"proxy"`
	DataDir     string `yaml:"data-dir"`
	APIAddress  string `yaml:"api-address"`
	AdminUserID int64  `yaml:"admin-user-id"`
	MaxThreads  int    `yaml:"max-threads"`
	BotToken    string `yaml:"bot-token"`
}

func main() {

	// 创建cli.app实例
	app := cli.NewApp()
	app.Name = "Telegraph Downloader"
	app.Usage = "Download images from Telegram Message or Telegraph links"
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "config",
			Aliases: []string{"c"},
			Value:   CONFIG_FILE,
			Usage:   "Config file path",
		},
		&cli.StringFlag{
			Name:    "bot-token",
			Aliases: []string{"token"},
			Value:   "",
			Usage:   "Telegram Bot Token",
		},
		&cli.Int64Flag{
			Name:    "admin-user-id",
			Aliases: []string{"admin", "id"},
			Value:   ADMIN_USER_ID,
			Usage:   "Admin user ID to allow use this bot",
		},
		&cli.StringFlag{
			Name:    "data-dir",
			Aliases: []string{"d"},
			Value:   DATA_DIR,
			Usage:   "Data directory to save images",
		},
		&cli.StringFlag{
			Name:    "api-address",
			Aliases: []string{"api"},
			Value:   TELEGRAM_API,
			Usage:   "API address of Telegram",
		},
		&cli.IntFlag{
			Name:    "max-threads",
			Aliases: []string{"threads"},
			Value:   MAX_THREADS,
			Usage:   "Max threads to download images",
		},
		&cli.StringFlag{
			Name:  "proxy",
			Value: "none",
			Usage: "Proxy address, e.g. socks5://127.0.0.1:1080",
		},
	}
	app.Before = func(c *cli.Context) (err error) {
		// 获取配置
		cfg, err = LoadConfig(c.String("config"))
		if err != nil {
			log.Printf("无法加载配置文件: %v\n", err)
			cfg = &config{
				ProxyString: c.String("proxy"),
				DataDir:     c.String("data-dir"),
				APIAddress:  c.String("api-address"),
				AdminUserID: c.Int64("admin-user-id"),
				MaxThreads:  c.Int("max-threads"),
				BotToken:    c.String("bot-token"),
			}
		} else {
			if c.IsSet("proxy") || cfg.ProxyString == "" {
				cfg.ProxyString = c.String("proxy")
			}
			if c.IsSet("data-dir") || cfg.DataDir == "" {
				cfg.DataDir = c.String("data-dir")
			}
			if c.IsSet("api-address") || cfg.APIAddress == "" {
				cfg.APIAddress = c.String("api-address")
			}
			if c.IsSet("admin-user-id") || cfg.AdminUserID == 0 {
				cfg.AdminUserID = c.Int64("admin-user-id")
			}
			if c.IsSet("max-threads") || cfg.MaxThreads == 0 {
				cfg.MaxThreads = c.Int("max-threads")
			}
			if c.IsSet("bot-token") {
				cfg.BotToken = c.String("bot-token")
			}
		}
		if cfg.BotToken == "" {
			return fmt.Errorf("bot-token is required, please set it in config file or command line")
		}
		return nil
	}
	app.Action = func(c *cli.Context) error {

		// 设置Telebot配置
		pref := telebot.Settings{
			Token:  cfg.BotToken,
			URL:    cfg.APIAddress, // 使用自定义的API地址
			Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
		}

		// 创建Bot实例
		bot, err := telebot.NewBot(pref)
		if err != nil {
			log.Fatalf("无法创建Bot: %v", err)
		}
		log.Printf("Bot已启动: %s\n", bot.Me.Username)
		// 处理消息

		bot.Use(func(hf telebot.HandlerFunc) telebot.HandlerFunc { // 添加中间件
			return func(c telebot.Context) error {
				if c.Sender().ID != cfg.AdminUserID && cfg.AdminUserID != -1 {
					log.Printf("非管理员用户 %s 尝试使用机器人\n", c.Sender().Username)
					return c.Reply("抱歉，只有指定用户才能使用本机器人。")
				}
				return hf(c)
			}
		})

		bot.Handle(telebot.OnText, func(c telebot.Context) error {
			// 检查消息内容是否包含Telegraph链接
			if len(c.Message().Entities) != 0 {
				for _, e := range c.Message().Entities {
					if e.Type == telebot.EntityTextLink {
						msg, _ := c.Bot().Reply(c.Message(), fmt.Sprintf("检测到链接: %s, 正在保存...", e.URL), telebot.NoPreview)
						// 爬取并下载Telegraph页面中的图片
						title, err := downloadTelegraphImages(e.URL)
						if err != nil {
							log.Printf("保存%s时出错: %v\n", title, err)
							_, err = c.Bot().Edit(msg, fmt.Sprintf("保存%s时出错: %v", title, err))
							return err
						}
						log.Printf("保存%s完成。\n", title)
						_, err = c.Bot().Edit(msg, fmt.Sprintf("保存%s完成。", title))
						return err
					}
				}
			}

			// 解析消息内容，提取Telegraph链接
			telegraphURL := extractTelegraphURL(c.Text())
			if telegraphURL == "" {
				return c.Reply("未找到有效的Telegraph链接。")
			}
			msg, _ := c.Bot().Reply(c.Message(), fmt.Sprintf("检测到链接: %s, 正在保存...", telegraphURL), telebot.NoPreview)
			// 爬取并下载Telegraph页面中的图片
			title, err := downloadTelegraphImages(telegraphURL)
			if err != nil {
				log.Printf("保存%s时出错: %v\n", title, err)
				_, err = c.Bot().Edit(msg, fmt.Sprintf("保存%s时出错: %v", title, err))
				return err
			}
			log.Printf("保存%s完成。\n", title)
			_, err = c.Bot().Edit(msg, fmt.Sprintf("保存%s完成。", title))
			return err
		})

		// 启动Bot
		bot.Start()
		return nil
	}

	// 运行cli.app
	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}

}

// 提取消息中的Telegraph链接
func extractTelegraphURL(text string) string {
	re := regexp.MustCompile(`https?://telegra\.ph/[^\s]+`)
	return re.FindString(text)
}

// 爬取Telegraph页面并下载图片
func downloadTelegraphImages(url string) (title string, err error) {
	// 请求Telegraph页面
	res, err := HttpGet(url, cfg.ProxyString)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return "", fmt.Errorf("请求失败，状态码: %d", res.StatusCode)
	}

	// 解析页面
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return "", err
	}

	// 获取页面标题作为文件夹名称
	title = doc.Find("header h1").First().Text()
	if title == "" {
		return "", fmt.Errorf("未找到页面标题")
	}
	log.Printf("页面标题: %s\n", title)
	// 创建文件夹
	dirName := path.Join(cfg.DataDir, strings.TrimSpace(title))
	log.Printf("创建文件夹: %s\n", dirName)
	if err := os.MkdirAll(dirName, os.ModePerm); err != nil {
		return "", err
	}

	// 创建waitgroup，等待所有图片下载完成
	wg := &sync.WaitGroup{}
	// 使用channel控制并发数
	sem := make(chan struct{}, cfg.MaxThreads)
	for i := 0; i < cfg.MaxThreads; i++ {
		sem <- struct{}{}
	}
	// 查找并下载所有图片
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		imgURL, exists := s.Attr("src")
		if !exists {
			return
		}

		// 如果图片URL是相对路径，需要补全为绝对路径
		if !strings.HasPrefix(imgURL, "http") {
			imgURL = "https://telegra.ph" + imgURL
		}

		wg.Add(1)

		// 下载图片
		go downloadFile(fmt.Sprintf("%s/%d.jpg", dirName, i), imgURL, wg, sem)
	})
	wg.Wait()
	return title, nil
}

// downloadFile 使用代理下载文件并保存到指定路径，带有重试机制
func downloadFile(filepath string, fileURL string, wg *sync.WaitGroup, sem chan struct{}) {
	defer func() {
		wg.Done()
		sem <- struct{}{}
	}()
	<-sem
	const maxRetries = 3               // 最大重试次数
	const retryDelay = 2 * time.Second // 每次重试前的延迟时间

	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err = func() error {
			data, err := HttpGet(fileURL, cfg.ProxyString)
			if err != nil {
				return err
			}
			defer data.Body.Close()
			file, err := os.Create(filepath)
			if err != nil {
				return fmt.Errorf("无法创建文件: %v", err)
			}
			defer file.Close()

			_, err = file.ReadFrom(data.Body)
			if err != nil {
				return fmt.Errorf("写入文件失败: %v", err)
			}

			log.Printf("文件下载成功: %s", filepath)
			return nil
		}()

		if err == nil {
			break
		}

		if attempt < maxRetries {
			log.Printf("下载失败，正在重试 (%d/%d)... 错误: %v", attempt, maxRetries, err)
			time.Sleep(retryDelay)
		}
	}

	if err != nil {
		log.Printf("下载文件最终失败: %v", err)
	}
}

// HttpGet 发送一个HTTP GET请求，支持使用代理
func HttpGet(targetURL string, proxyString string) (*http.Response, error) {
	// 创建一个HTTP客户端
	var proxyClient *http.Client

	//构建代理连接
	switch strings.Split(proxyString, ":")[0] {
	case "socks5":
		proxyDialer, err := proxy.SOCKS5("tcp", strings.TrimPrefix(proxyString, "socks5://"), nil, proxy.Direct)
		if err != nil {
			log.Fatalf("[proxy]cannot parse proxy with error: %s\n", err)
		}
		proxyClient = &http.Client{Transport: &http.Transport{Dial: proxyDialer.Dial}}
	case "http", "https":
		proxyUrl, err := url.Parse(proxyString)
		if err != nil {
			log.Fatalf("[proxy]cannot parse proxy with error: %s\n", err)
		}
		proxyClient = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyUrl)}}
	default:
		proxyClient = &http.Client{}
	}

	// 创建HTTP请求
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	// 发送请求
	return proxyClient.Do(req)

}

// LoadConfig 加载配置文件
func LoadConfig(path string) (c *config, err error) {
	c = &config{}
	cFile, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return c, yaml.Unmarshal(cFile, c)
}
