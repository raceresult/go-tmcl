package tmcl

import "fmt"

// ApplicationStatus is the return value of GetApplicationStatus.
type ApplicationStatus int32

const (
	ApplicationStopped ApplicationStatus = 0
	ApplicationRunning ApplicationStatus = 1
	ApplicationStep    ApplicationStatus = 2
	ApplicationReset   ApplicationStatus = 3
)

func (s ApplicationStatus) String() string {
	switch s {
	case ApplicationStopped:
		return "stopped"
	case ApplicationRunning:
		return "running"
	case ApplicationStep:
		return "step"
	case ApplicationReset:
		return "reset"
	default:
		return fmt.Sprintf("unknown(%d)", int32(s))
	}
}
type MVPMode byte

const (
	ABS   MVPMode = 0 // move to absolute position
	REL   MVPMode = 1 // move to relative position
	COORD MVPMode = 2 // move to stored coordinate
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
	MVP(mode MVPMode, motor byte, value int32) error
	SAP(index byte, motor byte, value int32) error
	GAP(index byte, motor byte) (int32, error)
	STAP(index byte, motor byte) error
	RSAP(index byte, motor byte) error
	SGP(index byte, bank byte, value int32) error
	GGP(index byte, bank byte) (int32, error)
	STGP(index byte, bank byte) error
	RSGP(index byte, bank byte) error
	SIO(port byte, bank byte, value bool) error
	GIO(port byte, bank byte) (int32, error)
	StopApplication() error
	RunApplication(specificAddress bool, address int32) error
	StepApplication() error
	ResetApplication() error
	GetApplicationStatus() (ApplicationStatus, error)
	GetFirmwareVersion() (string, error)
}

// StopMotors stops motors 0 through count-1. It is safe to call before
// flashing a program to ensure no motors are running while the EEPROM is
// being written.
func (q *TMCL) StopMotors(count byte) error {
	for i := byte(0); i < count; i++ {
		if err := q.MST(i); err != nil {
			return fmt.Errorf("stop motor %d: %w", i, err)
		}
	}
	return nil
}

// ROR rotates the motor to the right at the given velocity.
func (q *TMCL) ROR(motor byte, velocity int32) error {
	_, err := q.Exec(1, 0, motor, velocity)

	return err
}

// ROL rotates the motor to the left at the given velocity.
func (q *TMCL) ROL(motor byte, velocity int32) error {
	_, err := q.Exec(2, 0, motor, velocity)

	return err
}

// MST stops the motor.
func (q *TMCL) MST(motor byte) error {
	_, err := q.Exec(3, 0, motor, 0)

	return err
}

// MVP moves an axis to the given position.
func (q *TMCL) MVP(mode MVPMode, motor byte, value int32) error {
	_, err := q.Exec(4, byte(mode), motor, value)

	return err
}

// SAP sets an axis parameter.
func (q *TMCL) SAP(index byte, motor byte, value int32) error {
	_, err := q.Exec(5, index, motor, value)

	return err
}

// GAP gets an axis parameter.
func (q *TMCL) GAP(index byte, motor byte) (int32, error) {
	return q.Exec(6, index, motor, 0)
}

// STAP stores an axis parameter to EEPROM.
func (q *TMCL) STAP(index byte, motor byte) error {
	_, err := q.Exec(7, index, motor, 0)

	return err
}

// RSAP restores an axis parameter from EEPROM.
func (q *TMCL) RSAP(index byte, motor byte) error {
	_, err := q.Exec(8, index, motor, 0)

	return err
}

// SGP sets a global parameter.
func (q *TMCL) SGP(index byte, bank byte, value int32) error {
	_, err := q.Exec(9, index, bank, value)

	return err
}

// GGP gets a global parameter.
func (q *TMCL) GGP(index byte, bank byte) (int32, error) {
	return q.Exec(10, index, bank, 0)
}

// STGP stores a global parameter to EEPROM.
func (q *TMCL) STGP(index byte, bank byte) error {
	_, err := q.Exec(11, index, bank, 0)
	return err
}

// RSGP restores a global parameter from EEPROM.
func (q *TMCL) RSGP(index byte, bank byte) error {
	_, err := q.Exec(12, index, bank, 0)
	return err
}

// SIO sets a digital output port.
func (q *TMCL) SIO(port byte, bank byte, value bool) error {
	var b int32 = 0
	if value {
		b = 1
	}

	_, err := q.Exec(14, port, bank, b)

	return err
}

// GIO gets the value of an input or output port.
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

// GetApplicationStatus returns the current state of the TMCL standalone application.
//
// The TMCM-351 firmware packs the state in the high byte of the reply value
// field. The lower bytes contain additional board state (e.g. the program
// counter) whose exact encoding is firmware-specific.
func (q *TMCL) GetApplicationStatus() (ApplicationStatus, error) {
	val, err := q.Exec(135, 0, 0, 0)
	if err != nil {
		return 0, err
	}
	return ApplicationStatus((uint32(val) >> 24) & 0xFF), nil
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
