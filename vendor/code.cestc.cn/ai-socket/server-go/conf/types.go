package conf

import (
	"errors"

	"github.com/fsnotify/fsnotify"
)

const (
	// RunModeDebug 调试
	RunModeDebug = "debug"

	// RunModeTest 测试
	RunModeTest = "test"

	// RunModeRelease 线上发布
	RunModeRelease = "release"
)

const (
	LoadModeLoaded LoadMode = iota
	LoadModeLoading
)

type LoadMode int

const (
	DeployModeCloud DeployMode = "Cloud"
	DeployModeEdge  DeployMode = "Edge"
)

type DeployMode string

// IsCloud 判断是否为云模式
func (dm DeployMode) IsCloud() bool {
	return dm == DeployModeCloud || dm == ""
}

// IsEdge 判断是否为边缘计算模式
func (dm DeployMode) IsEdge() bool {
	return dm == DeployModeEdge
}

const (
	AuthTypeFake       AuthType = "Fake"
	AuthTypeAuthCenter          = "AuthCenter"
)

type AuthType string

type Config struct {
	AppName    string
	AppCode    int
	RunMode    string
	Feature    any
	DeployMode DeployMode

	Services   []string
	HTTPServer HTTPServerConfig
	FakeAuth   FakeAuthConfig

	AllSettings          map[string]any
	LoadMode             LoadMode
	configContentChanged []func(newConfig *Config, changedKeys []string)
	configChanged        map[string]func(in fsnotify.Event)
}

// 检查配置文件是否合规
func (c *Config) check() error {
	if c.AppName == "" {
		return errors.New("Config.AppName is empty")
	}
	if c.AppCode == 0 {
		return errors.New("Config.AppCode is empty")
	}
	if c.RunMode == "" {
		return errors.New("Config.RunMode is empty")
	}

	c.setDefaults()
	return nil
}

func (c *Config) setDefaults() {
	if c.DeployMode == "" {
		c.DeployMode = defaultDeployMode
	}
	if c.HTTPServer.AuthType == "" {
		c.HTTPServer.AuthType = AuthTypeFake
	}
}

func (c *Config) reset(newConfig *Config) {
	c.AppName = newConfig.AppName
	c.AppCode = newConfig.AppCode
	c.RunMode = newConfig.RunMode
	c.DeployMode = newConfig.DeployMode
	c.Feature = newConfig.Feature

	c.Services = newConfig.Services
	c.HTTPServer = newConfig.HTTPServer
	c.FakeAuth = newConfig.FakeAuth
}

type FakeAuthConfig struct {
	OrgID    string
	UserID   string
	UserName string
	UserRole string
}

type HTTPServerConfig struct {
	Listen            string
	ReadTimeout       int // 单位 ms
	WriteTimeout      int // 单位 ms
	IdleTimeout       int
	MaxHeaderBytes    int
	APIBasePath       string
	IdempotenceEnable bool
	IdempotenceTTL    int // 单位 分钟
	PprofEnable       bool
	SwaggerEnable     bool
	Trace             HTTPServerTraceConfig
	UseH2C            bool // 支持HTTP2 无SSL
	AuthType          AuthType
	// ConsoleEnable 是否开启Console API反向代理功能
	// 该反向代理会将前端API请求（路径包含/api）转换为内部OpenAPI请求（路径包含/internal）
	// 例如：POST /example/api/v1/GetProject => POST /example/internal/v1/GetProject
	ConsoleEnable     bool
	ConsoleProxyNames []string
	// AltoHandlerAPIError 是否开启针对 /{appName}/api/* 接口的自动状态码处理功能
	// 与ConsoleEnable互斥，仅当ConsoleEnable=false时生效
	// 当开启时，会拦截所有 /{appName}/api/* 接口的响应：
	//   - 将所有非200的HTTP状态码统一改为200，但保持响应体内容不变
	//   - 对于 errors.Error 类型的错误，会规范化处理，自动设置 RequestID 并确保 StatusCode 字段正确设置
	AltoHandlerAPIError bool
	Assets              []HTTPServerAssetConfig
	//开启针对 /{appName}/api/* 接口的自动状态码处理功能 （白名单）
	AllowAPIPathNoHandlerAPIError []string
}

type HTTPServerAssetConfig struct {
	AuthEnable bool
	URLPath    string
	FilePath   string
}

type HTTPServerTraceConfig struct {
	PrefixFilters []string
	SuffixFilters []string
}

type ClientConfig struct {
	Endpoint          string
	AccessKey         string
	SecretKey         string
	Region            string
	Retry             int
	EnableImpersonate bool
	EnableIdempotence bool
	EnableSTS         bool
	STSDuration       int
	STSACL            string
}
