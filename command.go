package tmcl

import "fmt"

const (
	ABS   byte = 0
	REL   byte = 1
	COORD byte = 2
)

const (
	// DefaultSerialBaud is the default board baud.
	DefaultSerialBaud = 9600

	// GlobalParameterBank is the bank of the program variables.
	GlobalParameterBank = 2

	// DigitalInputBank is used with GIO and SIO for digital inputs.
	DigitalInputBank = 0

	// AnalogInputBank is the bank used for analog inputs.
	AnalogInputBank = 1

	// DigitalOutputBank is the bank used for controlling the outputs.
	DigitalOutputBank = 2
)

type Board interface {
	ROR(motor byte, velocity int32) error
	ROL(motor byte, velocity int32) error
	MST(motor byte) error
	MVP(mode byte, motor byte, value int32) error
	SAP(index byte, motor byte, value int32) error
	GAP(index byte, motor byte) (int32, error)
	STAP(index byte, motor byte) error
	RSAP(index byte, motor byte) error
	SGP(index byte, bank byte, value int32) error
	GGP(index byte, bank byte) (int32, error)
	STGP(index byte, bank byte) (int32, error)
	RSGP(index byte, bank byte) (int32, error)
	SIO(port byte, bank byte, value bool) error
	GIO(port byte, bank byte) (int32, error)
	StopApplication() error
	RunApplication(specificAddress bool, address int32) error
	StepApplication() error
	ResetApplication() error
	GetApplicationStatus() (int32, error)
	GetFirmwareVersion() (string, error)
}

// ROR is Rotate right
func (q *TMCL) ROR(motor byte, velocity int32) error {
	_, err := q.Exec(1, 0, motor, velocity)

	return err
}

// ROL is reotate left
func (q *TMCL) ROL(motor byte, velocity int32) error {
	_, err := q.Exec(2, 0, motor, velocity)

	return err
}

// MST is motor stop
func (q *TMCL) MST(motor byte) error {
	_, err := q.Exec(3, 0, motor, 0)

	return err
}

// MVP is moving an axis
func (q *TMCL) MVP(mode byte, motor byte, value int32) error {
	_, err := q.Exec(4, mode, motor, value)

	return err
}

// SAP is set axis parameter
func (q *TMCL) SAP(index byte, motor byte, value int32) error {
	_, err := q.Exec(5, index, motor, value)

	return err
}

// GAP is get axis parameter
func (q *TMCL) GAP(index byte, motor byte) (int32, error) {
	return q.Exec(6, index, motor, 0)
}

// STAP is store axis parameter
func (q *TMCL) STAP(index byte, motor byte) error {
	_, err := q.Exec(7, index, motor, 0)

	return err
}

// RSAP is restore axis parameter
func (q *TMCL) RSAP(index byte, motor byte) error {
	_, err := q.Exec(8, index, motor, 0)

	return err
}

// SGP is set global parameter
func (q *TMCL) SGP(index byte, bank byte, value int32) error {
	_, err := q.Exec(9, index, bank, value)

	return err
}

// GGP is get global parameter
func (q *TMCL) GGP(index byte, bank byte) (int32, error) {
	return q.Exec(10, index, bank, 0)
}

// STGP is store global parameter
func (q *TMCL) STGP(index byte, bank byte) (int32, error) {
	return q.Exec(11, index, bank, 0)
}

// RSGP is restore global parameter
func (q *TMCL) RSGP(index byte, bank byte) (int32, error) {
	return q.Exec(12, index, bank, 0)
}

// SIO is set io
func (q *TMCL) SIO(port byte, bank byte, value bool) error {
	var b int32 = 0
	if value {
		b = 1
	}

	_, err := q.Exec(14, port, bank, b)

	return err
}

// GIO is get io
func (q *TMCL) GIO(port byte, bank byte) (int32, error) {
	return q.Exec(15, port, bank, 0)
}

// StopApplication stops a running TMCL standalone application.
func (q *TMCL) StopApplication() error {
	_, err := q.Exec(128, 0, 0, 0)

	return err
}

// RunApplication starts the TMCL application.
// Optionally an address can be supplied where to start the program,
// otherwise the program is resumed at the current address.
func (q *TMCL) RunApplication(specificAddress bool, address int32) error {
	useAddr := byte(0)
	if specificAddress {
		useAddr = 1
	}

	_, err := q.Exec(129, useAddr, 0, address)

	return err
}

// StepApplication executes only the next command of a TMCL application.
func (q *TMCL) StepApplication() error {
	_, err := q.Exec(130, 0, 0, 0)

	return err
}

// ResetApplication sets the program counter to zero and stops the standalone application.
func (q *TMCL) ResetApplication() error {
	_, err := q.Exec(131, 0, 0, 0)

	return err
}

// GetApplicationStatus requests the TMCL application status.
// Returns:
// 0 – stop
// 1 – run
// 2 – step
// 3 – reset
func (q *TMCL) GetApplicationStatus() (int32, error) {
	val, err := q.Exec(135, 0, 0, 0)
	if err != nil {
		return 0, err
	}

	return int32((uint32(val) >> 24) & 0xFF), nil
}

// GetFirmwareVersion requests the firmware/version information.
func (q *TMCL) GetFirmwareVersion() (string, error) {
	format := byte(1) // always use byte format

	val, err := q.Exec(136, format, 0, 0)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%08X", val), nil
}
