package tmcl

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/tarm/serial"
)

const timeout = time.Second

var (
	errorWrongChecksum             = errors.New("wrong checksum")
	errorInvalidCommand            = errors.New("invalid command")
	errorWrongType                 = errors.New("wrong type")
	errorInvalidType               = errors.New("invalid type")
	errorConfigurationEEPROMLocked = errors.New("configuration EEPROM locked")
	errorCommandNotAvailable       = errors.New("command not available")
)

// TMCL is the main api object to connect to a TMCL board
type TMCL struct {
	port io.ReadWriter
	log  Logger

	cmdMutex sync.Mutex
}

// NewTMCL creates a new TMCL object
func NewTMCL(port io.ReadWriter, logger Logger) *TMCL {
	if logger == nil {
		logger = NoopLogger{}
	}

	return &TMCL{
		port: port,
		log:  logger,
	}
}

// Exec is the general function to call a command on the board
func (q *TMCL) Exec(cmd byte, typeNo byte, motorOrBank byte, value int32) (int32, error) {
	// one command at a time
	q.cmdMutex.Lock()
	defer q.cmdMutex.Unlock()

	if q.port == nil {
		return 0, errors.New("port not open")
	}

	var tx [9]byte
	tx[0] = 2 // module address not used
	tx[1] = cmd
	tx[2] = typeNo
	tx[3] = motorOrBank
	binary.BigEndian.PutUint32(tx[4:8], uint32(value))
	tx[8] = calcChecksum(tx[:8])

	// lg.Debug().Msgf("tmcl >>> %x (cmd: %d, index: %d, bank: %d, val: %d)", tx, typeNo, cmd, motorOrBank, value)

	// send
	if _, err := q.port.Write(tx[:]); err != nil {
		return 0, err
	}

	var resp [9]byte
	// This call depends on a timeout being set for the serial-port.
	if _, err := io.ReadFull(q.port, resp[:]); err != nil {
		return 0, err
	}

	returnValue := int32(binary.BigEndian.Uint32(resp[4:8]))
	// lg.Debug().Msgf("tmcl <<< %x (val: %d)", resp, returnValue)

	// check checksum
	if resp[8] != calcChecksum(resp[:8]) {
		return 0, errorWrongChecksum
	}

	// check status code
	if resp[2] < 100 {
		return 0, getError(resp[2])
	}

	// return result
	return returnValue, nil
}

// getError is a helper method to return meaningful errors.
func getError(code byte) error {
	switch code {
	case 100, 101:
		// 100: success
		// 101: Command loaded into TMCL program EEPROM
		return nil
	case 1:
		return errorWrongChecksum
	case 2:
		return errorInvalidCommand
	case 3:
		return errorWrongType
	case 4:
		return errorInvalidType
	case 5:
		return errorConfigurationEEPROMLocked
	case 6:
		return errorCommandNotAvailable
	default:
		return fmt.Errorf("board returned error code %d", code)
	}
}

// calcChecksum calculates the checksum by adding up all bytes
func calcChecksum(bts []byte) byte {
	var x byte
	for _, b := range bts {
		x += b
	}

	return x
}

// Serial is a TMCL board connected via serial.
type Serial struct {
	*TMCL

	serialPort *serial.Port
}

// NewSerial creates a new struct for a TMCL-Board that opens a serial itself.
func NewSerial() *Serial {
	return &Serial{
		TMCL: NewTMCL(nil, NoopLogger{}),
	}
}

// OpenPort opens the serial port.
func (q *Serial) OpenPort(comPort string, baudRate int) error {
	if q.serialPort != nil {
		return nil
	}

	c := &serial.Config{Name: comPort, Baud: baudRate, ReadTimeout: timeout}
	port, err := serial.OpenPort(c)
	if err != nil {
		return err
	}

	q.port = port
	q.serialPort = port

	return nil
}

// ClosePort closes the serial port. Do not call this method if you passed a port with UseExistingPort.
func (q *Serial) ClosePort() {
	if q.serialPort == nil {
		return
	}

	_ = q.serialPort.Close()
	q.port = nil
	q.serialPort = nil
}
