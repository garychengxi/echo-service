package console

import sdk "echo-service/sdk/go/echoservicev1"

type Service struct {
	Name, Version             string
	AuthenticationImplemented bool
}

func (s Service) GetServiceInfo() sdk.ServiceInfo {
	return sdk.ServiceInfo{
		Name: s.Name, Version: s.Version,
		ConsoleBasePath:    "/echo-service/api/v1",
		GatewayBasePath:    "/echo-service/gateway/v1",
		GatewayImplemented: false, AuthenticationImplemented: s.AuthenticationImplemented,
	}
}
