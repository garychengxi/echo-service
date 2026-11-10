package modbus

type API interface {
	GetRegister(slaveID uint8, regAddr int) (any, error)
	SetRegister(slaveID uint8, regAddr int, value any) error
}
