package internalsdk

import (
	"errors"
	"fmt"

	"resty.dev/v3"

	"code.cestc.cn/ai-socket/server-go/constants"
	core "code.cestc.cn/ai-socket/server-go/pkg"
	"code.cestc.cn/ai-socket/server-go/sdk"
)

type InternalClient struct {
	*sdk.Client
	Config  *InternalClientConfig
	Context *core.Context
}

type InternalClientConfig struct {
	*sdk.Config
	EnableImpersonate bool
	EnableIdempotence bool
	EnableSTS         bool
	STSDuration       int
	STSACL            string
}

func NewInternalClient(ak, sk, endpoint string) (*InternalClient, error) {
	if endpoint == "" {
		return nil, errors.New("endpoint should not be empty")
	}

	if ak == "" || sk == "" {
		return nil, errors.New("ak and sk should no be empty")
	}

	config := &InternalClientConfig{
		Config: &sdk.Config{
			Endpoint: endpoint,
			Timeout:  30,
		},
	}

	return NewInternalClientWithConfig(config), nil
}

func NewInternalClientWithConfig(config *InternalClientConfig) *InternalClient {
	c := sdk.NewClientWithConfig(config.Config)
	ic := &InternalClient{
		Client:  c,
		Config:  config,
		Context: &core.Context{},
	}
	return ic
}

func Wrap(in any, ctx *core.Context) *InternalClient {
	switch t := in.(type) {
	case *InternalClient:
		return wrapI(t, ctx).registerMiddleware()
	case *sdk.Client:
		return wrapC(t, ctx).registerMiddleware()
	default:
		return nil
	}
}

func wrapI(c *InternalClient, ctx *core.Context) *InternalClient {
	return &InternalClient{
		Client:  c.Client.Clone(),
		Config:  c.Config,
		Context: ctx,
	}
}

func wrapC(c *sdk.Client, ctx *core.Context) *InternalClient {
	config := &InternalClientConfig{
		Config:            c.Config,
		EnableImpersonate: true,
		EnableIdempotence: true,
	}
	return &InternalClient{
		Client:  c.Clone(),
		Config:  config,
		Context: ctx,
	}
}

func (c *InternalClient) registerMiddleware() *InternalClient {
	c.Client.AddRequestMiddleware(func(client *resty.Client, req *resty.Request) error {
		c.setImpersonateHeaders(req)
		return nil
	})

	return c
}

// setImpersonateHeaders 在请求级别设置 impersonate header
func (c *InternalClient) setImpersonateHeaders(req *resty.Request) {
	if c.Config.EnableImpersonate {
		req.SetHeader(constants.HeaderImpersonateOrgID, c.Context.GetString(constants.KeyOrgID))
		req.SetHeader(constants.HeaderImpersonateUserID, c.Context.GetString(constants.KeyUserID))
	} else if c.Config.EnableSTS {
		fmt.Println("TODO")
		// cred := c.Client.Config.Credentials
		// client, err := sts.NewStsClient(cred.AccessKeyId, cred.SecretAccessKey, c.Client.Config.Endpoint)
		// if err != nil {
		// 	return err
		// }

		// token, err := client.GetSessionToken(c.Config.STSDuration, c.Config.STSACL)
		// if err != nil {
		// 	return err
		// }

		// stsCred, err := NewCredentials(token.AccessKeyId, token.SecretAccessKey)
		// if err != nil {
		// 	return err
		// }
		// c.Client.Config.Credentials = stsCred
	}
}
