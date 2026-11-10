// Package echoservicev1 defines the console wire contract.
package echoservicev1

type Error struct {
	Code       string `json:"code"`
	RequestID  string `json:"requestId"`
	Message    string `json:"message,omitempty"`
	StatusCode int    `json:"statusCode"`
}

type Response[T any] struct {
	Data  T     `json:"data"`
	Error Error `json:"error"`
}

type ServiceInfo struct {
	Name                      string `json:"name"`
	Version                   string `json:"version"`
	ConsoleBasePath           string `json:"consoleBasePath"`
	GatewayBasePath           string `json:"gatewayBasePath"`
	GatewayImplemented        bool   `json:"gatewayImplemented"`
	AuthenticationImplemented bool   `json:"authenticationImplemented"`
}
