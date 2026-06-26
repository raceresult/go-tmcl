package tmcl

import "fmt"

type Logger interface {
	LogSend(raw []byte, cmd, index, bank byte, val int32)
	LogRecv(raw []byte, val int32)
}

type NoopLogger struct{}

func (NoopLogger) LogSend(raw []byte, cmd, index, bank byte, val int32) {}
func (NoopLogger) LogRecv(raw []byte, val int32)                        {}

type DefaultLogger struct{}

func (DefaultLogger) LogSend(raw []byte, cmd, index, bank byte, val int32) {
	fmt.Printf("tmcl >>> %x (cmd: %d, index: %d, bank: %d, val: %d)\n", raw, cmd, index, bank, val)
}

func (DefaultLogger) LogRecv(raw []byte, val int32) {
	fmt.Printf("tmcl <<< %x (val: %d)\n", raw, val)
}
