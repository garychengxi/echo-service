package license_manager

import (
	"errors"
	"time"

	"resty.dev/v3"
)

// Client License Manager SDK 客户端
type Client struct {
	endpoints    []string
	httpClient   *resty.Client
	currentIndex int
}

// Config 客户端配置
type Config struct {
	// Endpoints Client 访问地址 ip+端口，支持传递多个
	Endpoints []string
}

var _ LicenseManagerAPI = (*Client)(nil)

func NewClient(endpoints []string) (*Client, error) {
	if len(endpoints) == 0 {
		return nil, errors.New("endpoints should not be empty")
	}

	return NewClientWithConfig(&Config{
		Endpoints: endpoints,
	}), nil
}

func NewClientWithConfig(config *Config) *Client {
	client := resty.New().
		SetTimeout(15*time.Second).
		SetHeader("Content-Type", "application/json")

	return &Client{
		endpoints:    config.Endpoints,
		httpClient:   client,
		currentIndex: 0,
	}
}

func (c *Client) R() *resty.Request {
	return c.httpClient.R().SetHeader("Content-Type", "application/json")
}

// getBaseURL 获取当前endpoint的基础 URL
func (c *Client) getBaseURL() string {
	return "http://" + c.endpoints[c.currentIndex]
}

// nextEndpoint 切换到下一个endpoint
func (c *Client) nextEndpoint() {
	c.currentIndex = (c.currentIndex + 1) % len(c.endpoints)
}
