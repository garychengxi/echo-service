package modbus

import (
	"context"
	"encoding/binary"
	"math"
	"strconv"

	"code.cestc.cn/ai-socket/logger-go"
	"github.com/tbrandon/mbserver"
)

type DefaultModbusService struct {
	// Modbus协议文档，参见 https://www.modbustools.com/modbus.html
	Server *mbserver.Server

	API     API
	Port    int
	Address string
}

func NewDefaultModbusService(address string, port int, api API) DefaultModbusService {
	tcpServer := mbserver.NewServer()

	svc := DefaultModbusService{
		Server:  tcpServer,
		Port:    port,
		API:     api,
		Address: address,
	}
	// 重新注册功能码执行方法，除3功能码之外的功能码，均返回功能码错误信息
	tcpServer.RegisterFunctionHandler(1, svc.ReadCoils)
	tcpServer.RegisterFunctionHandler(2, svc.ReadDiscreteInputs)
	tcpServer.RegisterFunctionHandler(3, svc.ReadHoldingRegisters)
	tcpServer.RegisterFunctionHandler(4, svc.ReadInputRegisters)
	tcpServer.RegisterFunctionHandler(5, svc.WriteSingleCoil)
	tcpServer.RegisterFunctionHandler(6, svc.WriteHoldingRegister)
	tcpServer.RegisterFunctionHandler(15, svc.WriteMultipleCoils)
	tcpServer.RegisterFunctionHandler(16, svc.WriteHoldingRegisters)

	return svc
}

func (svc *DefaultModbusService) StartAsync() {
	go func() {
		err := svc.Server.ListenTCP(svc.Address + ":" + strconv.Itoa(svc.Port))
		if err != nil {
			logger.Fatalf(context.Background(), "StartServerError", err)
		}
	}()
}

func (svc *DefaultModbusService) StopAsync() {
	go func() {
		svc.Server.Close()
	}()
}

// 根据需求，目前仅实现保持寄存器读取单个寄存器功能（功能码3）
// 为避免其他请求读取到空的内存值作为正常返回，因此屏蔽掉其他功能码的实现函数

func (svc *DefaultModbusService) ReadCoils(
	s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	return []byte{}, &mbserver.IllegalFunction
}

func (svc *DefaultModbusService) ReadDiscreteInputs(
	s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	return []byte{}, &mbserver.IllegalFunction
}

func (svc *DefaultModbusService) ReadHoldingRegisters(
	s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	tcpFrame := frame.(*mbserver.TCPFrame)
	register, numRegs, endRegister := registerAddressAndNumber(frame)
	if endRegister > 65536 {
		return []byte{}, &mbserver.IllegalDataAddress
	}
	// read from db, check length
	bytes := make([]byte, numRegs*2)
	bytesNum := 0
	for regNum := 0; regNum < numRegs; regNum++ {
		regBytes, byteNum, err := svc.readDBData(tcpFrame.Device, register+regNum)
		if err != mbserver.Success {
			return []byte{}, &err
		}
		regNum += byteNum/2 - 1
		copy(bytes[bytesNum:], regBytes[:byteNum])
		bytesNum += byteNum
		if bytesNum >= numRegs*2 {
			break
		}
	}
	return append([]byte{byte(numRegs * 2)}, bytes[:numRegs*2]...), &mbserver.Success
}

func (svc *DefaultModbusService) ReadInputRegisters(
	s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	return []byte{}, &mbserver.IllegalFunction
}

func (svc *DefaultModbusService) WriteSingleCoil(
	s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	return []byte{}, &mbserver.IllegalFunction
}

func (svc *DefaultModbusService) WriteHoldingRegister(
	s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	return []byte{}, &mbserver.IllegalFunction
}

func (svc *DefaultModbusService) WriteMultipleCoils(
	s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	return []byte{}, &mbserver.IllegalFunction
}

func (svc *DefaultModbusService) WriteHoldingRegisters(
	s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	return []byte{}, &mbserver.IllegalFunction
}

// 通用数据帧解析
func registerAddressAndNumber(frame mbserver.Framer) (register int, numRegs int, endRegister int) {
	data := frame.GetData()
	// Framer接口在rtu和tcp两种情况下的实现，都将各自数据包中功能码之后的数据保存在 frame.data 里，
	// 两种情况下，data中的数据格式一致，均为 2字节寄存器地址 + 2字节寄存器数量N + 2*N字节数据
	register = int(binary.BigEndian.Uint16(data[0:2]))
	numRegs = int(binary.BigEndian.Uint16(data[2:4]))
	endRegister = register + numRegs
	return register, numRegs, endRegister
}

func registerAddressAndValue(frame mbserver.Framer) (int, uint16) {
	// Framer接口在rtu和tcp两种情况下的实现，都将各自数据包中功能码之后的数据保存在 frame.data 里，
	// 两种情况下，data中的数据格式一致，均为 2字节寄存器地址 + 2字节数据
	data := frame.GetData()
	register := int(binary.BigEndian.Uint16(data[0:2]))
	value := binary.BigEndian.Uint16(data[2:4])
	return register, value
}

// 读取数据，并根据数据类型写入寄存器
func (svc *DefaultModbusService) readDBData(slaveID uint8, regAddr int) ([]byte, int, mbserver.Exception) {
	regValue, err := svc.API.GetRegister(slaveID, regAddr)
	bytes := make([]byte, 8)
	if err != nil {
		return bytes, 0, mbserver.IllegalDataAddress
	}

	boolValue, ok := regValue.(bool)
	if ok {
		if boolValue {
			bytes[0] = byte(1)
		}
		bytes[0] = byte(0)
		return bytes, 1, mbserver.Success
	}

	int16Value, ok := regValue.(int16)
	if ok {
		binary.BigEndian.PutUint16(bytes, uint16(int16Value))
		return bytes, 2, mbserver.Success
	}

	uint16Value, ok := regValue.(uint16)
	if ok {
		binary.BigEndian.PutUint16(bytes, uint16Value)
		return bytes, 2, mbserver.Success
	}

	int32Value, ok := regValue.(int32)
	if ok {
		binary.BigEndian.PutUint32(bytes, uint32(int32Value))
		return bytes, 4, mbserver.Success
	}

	uint32Value, ok := regValue.(uint32)
	if ok {
		binary.BigEndian.PutUint32(bytes, uint32Value)
		return bytes, 4, mbserver.Success
	}

	float32Value, ok := regValue.(float32)
	if ok {
		frameValue1 := float32(float32Value)
		binary.BigEndian.PutUint32(bytes, math.Float32bits(frameValue1))
		return bytes, 4, mbserver.Success
	}

	int64Value, ok := regValue.(int64)
	if ok {
		binary.BigEndian.PutUint64(bytes, uint64(int64Value))
		return bytes, 8, mbserver.Success
	}

	uint64Value, ok := regValue.(uint64)
	if ok {
		binary.BigEndian.PutUint64(bytes, uint64Value)
		return bytes, 8, mbserver.Success
	}

	float64Value, ok := regValue.(float64)
	if ok {
		binary.BigEndian.PutUint64(bytes, math.Float64bits(float64Value))
		return bytes, 8, mbserver.Success
	}
	return bytes, 0, mbserver.IllegalDataValue
}
