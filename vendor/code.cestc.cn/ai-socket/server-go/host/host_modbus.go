package host

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"code.cestc.cn/ai-socket/logger-go"

	"code.cestc.cn/ai-socket/server-go/pkg/modbus"
)

type ModbusHost struct {
	Services []modbus.DefaultModbusService
}

func NewModbusHost() *ModbusHost {
	host := &ModbusHost{}
	return host
}

func (h *ModbusHost) Run() {
	h.startServer()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	h.stopServer()
}

func (h *ModbusHost) RunAsync() {
	go func() {
		h.Run()
	}()
}

func (h *ModbusHost) AddService(services ...modbus.DefaultModbusService) {
	for _, s := range services {
		h.Services = append(h.Services, s)
	}
}

func (h *ModbusHost) startServer() {
	for _, svc := range h.Services {
		svc.StartAsync()
		logger.Infof(context.Background(), "modbusServer started at: %s",
			svc.Address+":"+strconv.FormatInt(int64(svc.Port), 10))
	}
}

func (h *ModbusHost) stopServer() {
	for _, svc := range h.Services {
		svc.StopAsync()
	}
}
