package sdk

import (
	"net/http"
	"time"

	"resty.dev/v3"
)

const (
	AuthTypeAPIKey = "api_key"
	AuthTypeAKSK   = "ak_sk"
)

type Credentials struct {
	// 通用字段
	AuthType string

	// API Key 方式
	APIKey string

	// AK/SK 方式
	AccessKeyId string

	SecretAccessKey string
}

type Config struct {
	// Endpoint Client访问地址，可以带URLPath，例如：http://0.0.0.0:80/v1
	Endpoint string

	ProxyURL string
	Retry    int
	Timeout  int

	Credentials *Credentials
}

type Client struct {
	*resty.Client
	Config *Config
}

func (c *Client) Clone() *Client {
	newClient := NewClientWithConfig(c.Config)
	originalTransport := c.Client.Transport()
	if originalTransport != nil {
		newClient.Client.SetTransport(originalTransport)
	} else {
		newClient.Client.SetTransport(http.DefaultTransport)
	}
	return newClient
}

func NewClientWithConfig(config *Config) *Client {
	client := resty.New().SetBaseURL(config.Endpoint)

	if config.ProxyURL != "" {
		client = client.SetProxy(config.ProxyURL)
	}
	if config.Timeout > 0 {
		client = client.SetTimeout(time.Duration(config.Timeout) * time.Second)
	}

	if config.Retry > 0 {
		client = client.SetRetryCount(config.Retry).
			SetRetryWaitTime(2 * time.Second).
			SetRetryMaxWaitTime(10 * time.Second)
	}
	if config.Credentials != nil {
		switch config.Credentials.AuthType {
		case AuthTypeAPIKey:
			client = client.SetAuthToken(config.Credentials.APIKey)
		case AuthTypeAKSK:
			// TODO
		}
	}

	return &Client{Client: client, Config: config}
}
