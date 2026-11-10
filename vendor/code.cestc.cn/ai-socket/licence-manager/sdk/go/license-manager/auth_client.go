package license_manager

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"code.cestc.cn/ai-socket/licence-manager/internal/crt"
)

const (
	// ROUTE_PATH_AUTHENTICATE 认证接口路径
	ROUTE_PATH_AUTHENTICATE = "/api/v1/althorizeInstance"
	// DEFAULT_INTERVAL_IN_SECOND 默认认证间隔（秒）
	DEFAULT_INTERVAL_IN_SECOND = 30
	// DEFAULT_TIMEOUT_IN_SECOND 默认请求超时时间（秒）
	DEFAULT_TIMEOUT_IN_SECOND = 5
	// SERVER_STATE_ACTIVE 服务器状态：活跃
	SERVER_STATE_ACTIVE = "active"
	// SERVER_STATE_STANDBY 服务器状态：备用
	SERVER_STATE_STANDBY = "standby"
	// SERVER_STATE_REJECT 服务器状态：拒绝
	SERVER_STATE_REJECT = "reject"
	// RESULT_STATE_SUCCESS 结果状态：成功
	RESULT_STATE_SUCCESS = "success"
	// RESULT_STATE_FAIL 结果状态：失败
	RESULT_STATE_FAIL = "fail"
)

// AuthorizationRequest 认证请求结构
type AuthorizationRequest struct {
	ModuleName string `json:"moduleName"`
	InstanceID string `json:"instanceID"`
}

// AuthorizationResponse 认证响应结构
type AuthorizationResponse struct {
	ServerState   string            `json:"serverState"`
	ResultState   string            `json:"resultState"`
	MetaConfigMap map[string]string `json:"metaConfigMap"`
	ErrorMessage  string            `json:"errorMessage"`
	MasterIP      string            `json:"masterIP"`
}

// AuthClient 认证客户端
type AuthClient struct {
	// ModuleName 模块名称，初始化时必须指定
	moduleName string
	// InstanceID 实例ID，随机生成的UUID，保存在内存中
	instanceID string
	// Endpoints 服务器端点列表，支持多个endpoint
	endpoints []string
	// IntervalInSecond 认证间隔（秒）
	intervalInSecond int
	// TimeoutInSecond 请求超时时间（秒）
	timeoutInSecond int
	// MasterIP 活跃master的IP地址，当ServerState不为active时从响应中获取
	masterIP string
	// ActiveEndpoint 当前活跃的endpoint
	activeEndpoint string
	// LastResponse 最后一次认证响应
	lastResponse AuthorizationResponse
	// ClientError 客户端错误
	clientError error
	// Mutex 互斥锁，保护并发访问
	mutex sync.RWMutex
	// Started 是否已启动
	started bool
	// StopChan 停止通道
	stopChan chan struct{}
	// StopOnce 确保只关闭一次通道
	stopOnce sync.Once
}

// Config 客户端配置
type AuthClientConfig struct {
	// ModuleName 模块名称（必需）
	ModuleName string
	// Endpoints 服务器端点列表，格式为 "host:port"（必需）
	Endpoints []string
	// TimeoutInSecond 请求超时时间（秒），默认5秒
	TimeoutInSecond int
}

// NewAuthClient 创建新的认证客户端
// 必须指定 ModuleName，InstanceID 会自动生成UUID
func NewAuthClient(config *AuthClientConfig) (*AuthClient, error) {
	if config == nil {
		return nil, errors.New("config cannot be nil")
	}
	if config.ModuleName == "" {
		return nil, errors.New("ModuleName is required")
	}
	if len(config.Endpoints) == 0 {
		return nil, errors.New("at least one endpoint is required")
	}

	// 生成UUID作为InstanceID
	instanceID := uuid.New().String()

	// 不允许指定interval
	interval := DEFAULT_INTERVAL_IN_SECOND

	timeout := config.TimeoutInSecond
	if timeout <= 0 {
		timeout = DEFAULT_TIMEOUT_IN_SECOND
	}

	return &AuthClient{
		moduleName:       config.ModuleName,
		instanceID:       instanceID,
		endpoints:        config.Endpoints,
		intervalInSecond: interval,
		timeoutInSecond:  timeout,
		stopChan:         make(chan struct{}),
	}, nil
}

// Start 启动认证客户端，开始定期认证
func (ac *AuthClient) Start() {
	ac.mutex.Lock()
	if ac.started {
		ac.mutex.Unlock()
		return
	}

	ac.started = true
	ac.mutex.Unlock()

	logrus.Debug("Auth client started!")

	// 立即执行一次认证（在锁外执行，避免死锁）
	ac.authenticate()

	// 启动定时认证
	go ac.startPeriodicAuth()
}

// Stop 停止认证客户端
func (ac *AuthClient) Stop() {
	ac.mutex.Lock()
	wasStarted := ac.started
	ac.started = false
	ac.mutex.Unlock()

	if !wasStarted {
		return
	}

	// 使用 sync.Once 确保通道只关闭一次
	ac.stopOnce.Do(func() {
		close(ac.stopChan)
	})
	logrus.Debug("Auth client stopped!")
}

// GetAuthorization 获取当前授权状态
func (ac *AuthClient) GetAuthorization() bool {
	ac.mutex.RLock()
	defer ac.mutex.RUnlock()
	return ac.lastResponse.ResultState == RESULT_STATE_SUCCESS
}

// GetMetaConfigMap 获取元配置映射
func (ac *AuthClient) GetMetaConfigMap() map[string]string {
	ac.mutex.RLock()
	defer ac.mutex.RUnlock()
	return ac.lastResponse.MetaConfigMap
}

// GetConfigMap 获取指定key的配置值
func (ac *AuthClient) GetConfigMap(key string) string {
	ac.mutex.RLock()
	defer ac.mutex.RUnlock()
	if ac.lastResponse.MetaConfigMap != nil {
		return ac.lastResponse.MetaConfigMap[key]
	}
	return ""
}

// ErrorMessage 获取错误信息
func (ac *AuthClient) ErrorMessage() string {
	ac.mutex.RLock()
	defer ac.mutex.RUnlock()
	if ac.clientError != nil {
		return ac.clientError.Error()
	}
	return ac.lastResponse.ErrorMessage
}

// GetLastResponse 获取最后一次认证响应
func (ac *AuthClient) GetLastResponse() AuthorizationResponse {
	ac.mutex.RLock()
	defer ac.mutex.RUnlock()
	return ac.lastResponse
}

// GetInstanceID 获取实例ID
func (ac *AuthClient) GetInstanceID() string {
	ac.mutex.RLock()
	defer ac.mutex.RUnlock()
	return ac.instanceID
}

// GetMasterIP 获取当前master IP
func (ac *AuthClient) GetMasterIP() string {
	ac.mutex.RLock()
	defer ac.mutex.RUnlock()
	return ac.masterIP
}

// startPeriodicAuth 启动定期认证
func (ac *AuthClient) startPeriodicAuth() {
	ticker := time.NewTicker(time.Duration(ac.intervalInSecond) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ac.authenticate()
		case <-ac.stopChan:
			return
		}
	}
}

// authenticate 执行认证
func (ac *AuthClient) authenticate() {
	logrus.Debug("Begin to authenticate!")

	ac.mutex.Lock()
	activeEndpoint := ac.activeEndpoint
	masterIP := ac.masterIP
	ac.mutex.Unlock()

	// 优先尝试连接活跃的masterIP
	if masterIP != "" {
		// 从endpoints中找到匹配masterIP的endpoint
		for _, endpoint := range ac.endpoints {
			if strings.HasPrefix(endpoint, masterIP+":") || endpoint == masterIP {
				resp, err := ac.doAuthenticate(endpoint)
				if err != nil {
					// 连接失败，记录错误但不立即更新响应，继续尝试其他endpoints
					logrus.Debugf("Failed to authenticate with master endpoint %s: %v, will try other endpoints", endpoint, err)
					// 清除masterIP，让后续逻辑尝试其他endpoints
					ac.mutex.Lock()
					ac.masterIP = ""
					ac.mutex.Unlock()
					break // 跳出循环，继续尝试其他endpoints
				}
				// 连接成功，检查响应状态
				if resp.ServerState == SERVER_STATE_ACTIVE {
					// 成功认证，但无论是否成功都要读取ErrorMessage
					if resp.ErrorMessage != "" {
						logrus.Debugf("Authenticated successfully with master endpoint: %s, but ErrorMessage is not empty: %s", endpoint, resp.ErrorMessage)
					}
					ac.updateResponse(resp, endpoint, nil)
					logrus.Debugf("Authenticated successfully with master endpoint: %s", endpoint)
					return
				}
				// 服务器返回了响应但状态不是active，需要更新响应和错误消息
				// 如果是standby状态且返回了MasterIP，立即切换到主节点并重新鉴权
				if resp.ServerState == SERVER_STATE_STANDBY && resp.MasterIP != "" {
					ac.mutex.Lock()
					ac.masterIP = resp.MasterIP
					ac.mutex.Unlock()
					logrus.Debugf("Server state is %s, switching to masterIP: %s", resp.ServerState, resp.MasterIP)

					// 从endpoints中找到匹配masterIP的endpoint并立即尝试连接
					masterFound := false
					for _, masterEndpoint := range ac.endpoints {
						if strings.HasPrefix(masterEndpoint, resp.MasterIP+":") || masterEndpoint == resp.MasterIP {
							// 跳过当前endpoint，避免重复连接
							if masterEndpoint == endpoint {
								continue
							}
							masterFound = true
							logrus.Debugf("Attempting to connect to master endpoint: %s (from standby endpoint: %s)", masterEndpoint, endpoint)
							masterResp, masterErr := ac.doAuthenticate(masterEndpoint)
							if masterErr != nil {
								logrus.Debugf("Failed to authenticate with master endpoint %s: %v", masterEndpoint, masterErr)
								// 主节点连接失败，继续尝试其他endpoints
								continue
							}
							// 如果主节点是active状态，立即返回成功
							if masterResp.ServerState == SERVER_STATE_ACTIVE {
								ac.updateResponse(masterResp, masterEndpoint, nil)
								logrus.Debugf("Successfully authenticated with master endpoint: %s", masterEndpoint)
								return
							}
							// 如果主节点也不是active，记录并继续处理
							logrus.Debugf("Master endpoint %s returned state: %s (not active)", masterEndpoint, masterResp.ServerState)
							if masterResp.MasterIP != "" {
								ac.mutex.Lock()
								ac.masterIP = masterResp.MasterIP
								ac.mutex.Unlock()
							}
							// 如果主节点返回standby，保存主节点的响应
							if masterResp.ServerState == SERVER_STATE_STANDBY {
								if masterResp.ErrorMessage == "" {
									masterResp.ErrorMessage = fmt.Sprintf("主节点也处于备用状态，无法处理鉴权请求。主节点IP: %s", masterResp.MasterIP)
								}
								ac.updateResponse(masterResp, "", nil)
							}
							// 继续尝试其他endpoints，不保存当前standby节点的响应
							break
						}
					}
					if !masterFound {
						logrus.Debugf("MasterIP %s not found in endpoints list: %v", resp.MasterIP, ac.endpoints)
					} else {
						// 如果找到了主节点（无论连接成功还是失败），都不保存standby响应，继续尝试其他endpoints
						logrus.Debugf("Master endpoint found, continuing to try other endpoints")
						continue
					}
				}
				// 如果服务器返回了错误消息，使用服务器的错误消息
				// 如果没有错误消息，根据服务器状态生成错误消息
				if resp.ErrorMessage == "" {
					if resp.ResultState == RESULT_STATE_FAIL {
						resp.ErrorMessage = fmt.Sprintf("鉴权失败: 服务器状态为 %s", resp.ServerState)
					} else if resp.ServerState == SERVER_STATE_STANDBY {
						resp.ErrorMessage = fmt.Sprintf("服务器处于备用状态，无法处理鉴权请求。主节点IP: %s", resp.MasterIP)
					} else if resp.ServerState == SERVER_STATE_REJECT {
						resp.ErrorMessage = "服务器拒绝鉴权请求"
					}
				}
				ac.updateResponse(resp, "", nil)
				// 如果是reject状态，停止尝试
				if resp.ServerState == SERVER_STATE_REJECT {
					logrus.Debugf("Server rejected at endpoint: %s, error: %s", endpoint, resp.ErrorMessage)
					return
				}
			}
		}
	}

	// 如果activeEndpoint存在，优先尝试
	if activeEndpoint != "" {
		resp, err := ac.doAuthenticate(activeEndpoint)
		if err != nil {
			// 连接失败，记录错误但不立即更新响应，继续尝试其他endpoints
			logrus.Debugf("Failed to authenticate with active endpoint %s: %v, will try other endpoints", activeEndpoint, err)
			// 清除activeEndpoint，让后续逻辑尝试其他endpoints
			ac.mutex.Lock()
			ac.activeEndpoint = ""
			ac.mutex.Unlock()
		} else {
			// 连接成功，检查响应状态
			if resp.ServerState == SERVER_STATE_ACTIVE {
				// 成功认证，但无论是否成功都要读取ErrorMessage
				if resp.ErrorMessage != "" {
					logrus.Debugf("Authenticated successfully with active endpoint: %s, but ErrorMessage is not empty: %s", activeEndpoint, resp.ErrorMessage)
				}
				ac.updateResponse(resp, activeEndpoint, nil)
				logrus.Debugf("Authenticated successfully with active endpoint: %s", activeEndpoint)
				return
			}
			// 服务器返回了响应但状态不是active，需要更新响应和错误消息
			// 如果是standby状态且返回了MasterIP，立即切换到主节点并重新鉴权
			if resp.ServerState == SERVER_STATE_STANDBY && resp.MasterIP != "" {
				ac.mutex.Lock()
				ac.masterIP = resp.MasterIP
				ac.mutex.Unlock()
				logrus.Debugf("Server state is %s, switching to masterIP: %s", resp.ServerState, resp.MasterIP)

				// 从endpoints中找到匹配masterIP的endpoint并立即尝试连接
				for _, masterEndpoint := range ac.endpoints {
					if strings.HasPrefix(masterEndpoint, resp.MasterIP+":") || masterEndpoint == resp.MasterIP {
						// 跳过当前endpoint，避免重复连接
						if masterEndpoint == activeEndpoint {
							continue
						}
						masterResp, masterErr := ac.doAuthenticate(masterEndpoint)
						if masterErr != nil {
							logrus.Debugf("Failed to authenticate with master endpoint %s: %v", masterEndpoint, masterErr)
							continue
						}
						// 如果主节点是active状态，立即返回成功
						if masterResp.ServerState == SERVER_STATE_ACTIVE {
							ac.updateResponse(masterResp, masterEndpoint, nil)
							logrus.Debugf("Successfully authenticated with master endpoint: %s", masterEndpoint)
							return
						}
						// 如果主节点也不是active，继续处理
						if masterResp.MasterIP != "" {
							ac.mutex.Lock()
							ac.masterIP = masterResp.MasterIP
							ac.mutex.Unlock()
						}
					}
				}
			}
			// 如果服务器返回了错误消息，使用服务器的错误消息
			// 如果没有错误消息，根据服务器状态生成错误消息
			if resp.ErrorMessage == "" {
				if resp.ResultState == RESULT_STATE_FAIL {
					resp.ErrorMessage = fmt.Sprintf("鉴权失败: 服务器状态为 %s", resp.ServerState)
				} else if resp.ServerState == SERVER_STATE_STANDBY {
					resp.ErrorMessage = fmt.Sprintf("服务器处于备用状态，无法处理鉴权请求。主节点IP: %s", resp.MasterIP)
				} else if resp.ServerState == SERVER_STATE_REJECT {
					resp.ErrorMessage = "服务器拒绝鉴权请求"
				}
			}
			ac.updateResponse(resp, "", nil)
			// 如果是reject状态，停止尝试
			if resp.ServerState == SERVER_STATE_REJECT {
				logrus.Debugf("Server rejected at endpoint: %s, error: %s", activeEndpoint, resp.ErrorMessage)
				return
			}
		}
	}

	// 遍历所有endpoints，找到active的master
	allFailed := true
	var failedEndpoints []string
	var endpointErrors []string
	for _, endpoint := range ac.endpoints {
		resp, err := ac.doAuthenticate(endpoint)
		if err != nil {
			// 连接失败，记录错误信息，但不立即更新响应，继续尝试其他endpoints
			failedEndpoints = append(failedEndpoints, endpoint)
			endpointErrors = append(endpointErrors, fmt.Sprintf("%s: %v", endpoint, err))
			logrus.Debugf("Failed to authenticate with endpoint %s: %v, will try other endpoints", endpoint, err)
			continue
		}

		allFailed = false

		// 如果ServerState不为active，保存MasterIP和响应
		if resp.ServerState != SERVER_STATE_ACTIVE {
			// 如果是standby状态且返回了MasterIP，立即切换到主节点并重新鉴权
			if resp.ServerState == SERVER_STATE_STANDBY && resp.MasterIP != "" {
				ac.mutex.Lock()
				ac.masterIP = resp.MasterIP
				ac.mutex.Unlock()
				logrus.Debugf("Server state is %s, switching to masterIP: %s", resp.ServerState, resp.MasterIP)

				// 从endpoints中找到匹配masterIP的endpoint并立即尝试连接
				masterFound := false
				masterSwitched := false
				for _, masterEndpoint := range ac.endpoints {
					if strings.HasPrefix(masterEndpoint, resp.MasterIP+":") || masterEndpoint == resp.MasterIP {
						// 跳过当前endpoint，避免重复连接
						if masterEndpoint == endpoint {
							continue
						}
						masterFound = true
						logrus.Debugf("Attempting to connect to master endpoint: %s", masterEndpoint)
						masterResp, masterErr := ac.doAuthenticate(masterEndpoint)
						if masterErr != nil {
							logrus.Debugf("Failed to authenticate with master endpoint %s: %v", masterEndpoint, masterErr)
							// 记录主节点连接失败，但不立即返回，继续尝试其他endpoints
							failedEndpoints = append(failedEndpoints, masterEndpoint)
							endpointErrors = append(endpointErrors, fmt.Sprintf("%s (master): %v", masterEndpoint, masterErr))
							continue
						}
						// 如果主节点是active状态，立即返回成功
						if masterResp.ServerState == SERVER_STATE_ACTIVE {
							ac.updateResponse(masterResp, masterEndpoint, nil)
							logrus.Debugf("Successfully authenticated with master endpoint: %s", masterEndpoint)
							return
						}
						// 如果主节点也不是active，记录并继续处理
						logrus.Debugf("Master endpoint %s returned state: %s", masterEndpoint, masterResp.ServerState)
						masterSwitched = true
						if masterResp.MasterIP != "" {
							ac.mutex.Lock()
							ac.masterIP = masterResp.MasterIP
							ac.mutex.Unlock()
						}
						// 如果主节点返回standby，保存主节点的响应而不是standby节点的响应
						if masterResp.ServerState == SERVER_STATE_STANDBY {
							// 保存主节点的响应，但继续尝试其他endpoints
							if masterResp.ErrorMessage == "" {
								masterResp.ErrorMessage = fmt.Sprintf("主节点也处于备用状态，无法处理鉴权请求。主节点IP: %s", masterResp.MasterIP)
							}
							ac.updateResponse(masterResp, "", nil)
						}
						// 继续尝试其他endpoints，不保存当前standby节点的响应
						break
					}
				}
				if !masterFound {
					logrus.Debugf("MasterIP %s not found in endpoints list: %v", resp.MasterIP, ac.endpoints)
					// 如果找不到主节点，保存standby响应
					if resp.ErrorMessage == "" {
						resp.ErrorMessage = fmt.Sprintf("服务器处于备用状态，无法处理鉴权请求。主节点IP: %s (未找到主节点endpoint)", resp.MasterIP)
					}
					ac.updateResponse(resp, "", nil)
				} else if !masterSwitched {
					// 如果找到了主节点但连接失败，不保存standby响应，继续尝试其他endpoints
					logrus.Debugf("Master endpoint found but connection failed, continuing to try other endpoints")
					continue
				} else {
					// 如果成功切换到主节点（即使主节点也是standby），已经保存了主节点的响应，继续尝试其他endpoints
					continue
				}
			} else if resp.MasterIP != "" {
				ac.mutex.Lock()
				ac.masterIP = resp.MasterIP
				ac.mutex.Unlock()
				logrus.Debugf("Server state is %s, saved masterIP: %s", resp.ServerState, resp.MasterIP)
			}
			// 如果服务器返回了错误消息，使用服务器的错误消息
			// 如果没有错误消息，根据服务器状态生成错误消息
			if resp.ErrorMessage == "" {
				if resp.ResultState == RESULT_STATE_FAIL {
					resp.ErrorMessage = fmt.Sprintf("鉴权失败: 服务器状态为 %s", resp.ServerState)
				} else if resp.ServerState == SERVER_STATE_STANDBY {
					resp.ErrorMessage = fmt.Sprintf("服务器处于备用状态，无法处理鉴权请求。主节点IP: %s", resp.MasterIP)
				} else if resp.ServerState == SERVER_STATE_REJECT {
					resp.ErrorMessage = "服务器拒绝鉴权请求"
				}
			}
			// 保存响应（standby和reject状态都需要保存）
			ac.updateResponse(resp, "", nil)
		}

		// 如果找到active的服务器，更新activeEndpoint
		if resp.ServerState == SERVER_STATE_ACTIVE {
			// 成功认证，但无论是否成功都要读取ErrorMessage
			if resp.ErrorMessage != "" {
				logrus.Debugf("Found active server at endpoint: %s, but ErrorMessage is not empty: %s", endpoint, resp.ErrorMessage)
			}
			ac.updateResponse(resp, endpoint, nil)
			logrus.Debugf("Found active server at endpoint: %s", endpoint)
			return
		}

		// 如果是reject状态，停止尝试
		if resp.ServerState == SERVER_STATE_REJECT {
			logrus.Debugf("Server rejected at endpoint: %s, error: %s", endpoint, resp.ErrorMessage)
			return
		}
	}

	// 如果所有endpoint都失败，更新错误状态和鉴权状态为失败
	if allFailed {
		var errMsg string
		if len(failedEndpoints) > 0 {
			errMsg = fmt.Sprintf("所有服务器端点连接失败，无法完成鉴权。失败的端点: %s。详细错误: %s",
				strings.Join(failedEndpoints, ", "), strings.Join(endpointErrors, "; "))
		} else {
			errMsg = "所有服务器端点连接失败，无法完成鉴权"
		}
		err := errors.New(errMsg)
		failResp := AuthorizationResponse{
			ResultState:  RESULT_STATE_FAIL,
			ErrorMessage: errMsg,
		}
		// 清除masterIP和activeEndpoint，因为所有服务器都不可用
		ac.mutex.Lock()
		ac.masterIP = ""
		ac.activeEndpoint = ""
		ac.mutex.Unlock()
		ac.updateResponse(failResp, "", err)
		logrus.Debugf("All endpoints failed, authentication state updated to fail. Cleared masterIP and activeEndpoint. Failed endpoints: %v", failedEndpoints)
	}
}

// updateResponse 更新响应状态
func (ac *AuthClient) updateResponse(resp AuthorizationResponse, activeEndpoint string, err error) {
	ac.mutex.Lock()
	defer ac.mutex.Unlock()
	ac.lastResponse = resp
	ac.activeEndpoint = activeEndpoint
	ac.clientError = err
	if resp.MasterIP != "" {
		ac.masterIP = resp.MasterIP
	}
}

// doAuthenticate 执行实际的认证请求
func (ac *AuthClient) doAuthenticate(endpoint string) (AuthorizationResponse, error) {
	req := AuthorizationRequest{
		ModuleName: ac.moduleName,
		InstanceID: ac.instanceID,
	}

	var resp AuthorizationResponse
	err := ac.httpRequest(&req, &resp, endpoint, ROUTE_PATH_AUTHENTICATE)
	if err != nil {
		return AuthorizationResponse{}, err
	}

	return resp, nil
}

// httpRequest 执行HTTPS请求
func (ac *AuthClient) httpRequest(req interface{}, resp interface{}, endpoint, routePath string) error {
	// 创建HTTP客户端
	client := &http.Client{
		Timeout: time.Duration(ac.timeoutInSecond) * time.Second,
	}

	// 使用默认CA证书配置TLS
	caCertBytes, _, err := crt.LoadCertificateBytes()
	if err != nil {
		return fmt.Errorf("加载默认CA证书失败: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertBytes) {
		return errors.New("解析默认CA证书失败")
	}
	client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:            pool,
			InsecureSkipVerify: true, // 跳过默认验证（包括hostname验证）
			// 自定义验证函数，手动验证证书是否由我们的CA签发（跳过hostname验证）
			VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return errors.New("未收到服务器证书")
				}
				// 解析服务器证书
				cert, err := x509.ParseCertificate(rawCerts[0])
				if err != nil {
					return fmt.Errorf("解析服务器证书失败: %w", err)
				}
				// 验证证书是否由我们的CA签发（跳过hostname验证）
				opts := x509.VerifyOptions{
					Roots:         pool,
					Intermediates: x509.NewCertPool(),
					// 不设置 DNSName，这样就不会验证 hostname
				}
				// 如果有中间证书，添加到中间证书池
				for i := 1; i < len(rawCerts); i++ {
					intermediateCert, err := x509.ParseCertificate(rawCerts[i])
					if err == nil {
						opts.Intermediates.AddCert(intermediateCert)
					}
				}
				_, err = cert.Verify(opts)
				if err != nil {
					return fmt.Errorf("证书验证失败: %w", err)
				}
				return nil
			},
		},
	}

	// 序列化请求
	var reqBytes []byte
	if req != nil {
		reqBytes, err = json.Marshal(req)
		if err != nil {
			return fmt.Errorf("序列化请求失败: %w", err)
		}
	} else {
		reqBytes = []byte("{}")
	}

	// 构建URL
	url := fmt.Sprintf("https://%s%s", endpoint, routePath)

	// 创建请求
	httpReq, err := http.NewRequest("POST", url, strings.NewReader(string(reqBytes)))
	if err != nil {
		return fmt.Errorf("创建HTTP请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// 执行请求
	httpResp, err := client.Do(httpReq)
	if err != nil {
		// 连接错误，提供更详细的错误信息
		// 检查是否是TLS相关错误
		errMsg := err.Error()
		if strings.Contains(errMsg, "certificate") || strings.Contains(errMsg, "x509") {
			return fmt.Errorf("TLS证书验证失败 (endpoint: %s, url: %s): %w", endpoint, url, err)
		}
		if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
			return fmt.Errorf("连接超时 (endpoint: %s, url: %s, timeout: %ds): %w", endpoint, url, ac.timeoutInSecond, err)
		}
		if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "no such host") {
			return fmt.Errorf("无法连接到服务器 (endpoint: %s, url: %s): %w", endpoint, url, err)
		}
		return fmt.Errorf("连接服务器失败 (endpoint: %s, url: %s): %w", endpoint, url, err)
	}
	defer httpResp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("读取服务器响应失败: %w", err)
	}

	// 检查HTTP状态码
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("服务器返回错误状态码: %d, 响应内容: %s", httpResp.StatusCode, string(body))
	}

	// 解析响应
	if err := json.Unmarshal(body, resp); err != nil {
		return fmt.Errorf("解析服务器响应失败: %w, 响应内容: %s", err, string(body))
	}

	return nil
}
