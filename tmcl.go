package tmcl

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/tarm/serial"
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

const timeout = time.Second

// TMCL is the main api object to connect to a TMCL board
type TMCL struct {
	ComPort  string
	baudRate int

	port     *serial.Port
	cmdMutex sync.Mutex
}

// NewTMCL creates a new TMCL object
func NewTMCL(comPort string, baudRate int) *TMCL {
	return &TMCL{
		ComPort:  comPort,
		baudRate: baudRate,
	}
}

// OpenPort opens the serial port
func (q *TMCL) OpenPort() error {
	if q.port != nil {
		return nil
	}

	c := &serial.Config{Name: q.ComPort, Baud: q.baudRate}
	port, err := serial.OpenPort(c)
	if err != nil {
		return err
	}
	q.port = port
	return nil
}

// ClosePort closes the serial port
func (q *TMCL) ClosePort() {
	if q.port == nil {
		return
	}
	_ = q.port.Close()
	q.port = nil
}

// Exec is the general function to call a command on the board
func (q *TMCL) Exec(cmd byte, typeNo byte, motorOrBank byte, value int) (int, error) {
	// one command at a time
	q.cmdMutex.Lock()
	defer q.cmdMutex.Unlock()

	// open port if not done yet
	if err := q.OpenPort(); err != nil {
		return 0, err
	}

	// create command
	bts := make([]byte, 9)
	bts[1] = cmd
	bts[2] = typeNo
	bts[3] = motorOrBank
	buff := new(bytes.Buffer)
	if err := binary.Write(buff, binary.BigEndian, uint32(value)); err != nil {
		return 0, err
	}
	bts[4] = buff.Bytes()[0]
	bts[5] = buff.Bytes()[1]
	bts[6] = buff.Bytes()[2]
	bts[7] = buff.Bytes()[3]

	// calc checksum
	bts[8] = calcChecksum(bts[:8])

	// send
	if _, err := q.port.Write(bts); err != nil {
		return 0, err
	}

	// wait for response
	start := time.Now()
	var buf []byte
	for {
		buf2 := make([]byte, 9)
		n, err := q.port.Read(buf2)
		if err != nil {
			return 0, err
		}
		if n != 0 {
			buf = append(buf, buf2[:n]...)
		}
		if len(buf) < 9 {
			if time.Since(start) > timeout {
				return 0, errors.New("timeout")
			}

			time.Sleep(time.Millisecond)
			continue
		}

		// check checksum
		if buf[8] != calcChecksum(buf[:8]) {
			return 0, errors.New("checksum invalid")
		}

		// check status code
		if buf[2] != 100 {
			return 0, errors.New("board returned error code " + strconv.Itoa(int(buf[2])))
		}

		// return result
		return int(binary.BigEndian.Uint32(buf[4:8])), nil
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
