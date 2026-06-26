package tmcl

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Download mode opcodes.
const (
	CmdStartDownload byte = 132 // Enter EEPROM program-download mode
	CmdEndDownload   byte = 133 // Exit download mode
)

// Standalone-program opcodes (for use inside ProgramInstruction).
const (
	OpcodeNOP   byte = 0
	OpcodeROR   byte = 1
	OpcodeROL   byte = 2
	OpcodeMST   byte = 3
	OpcodeMVP   byte = 4
	OpcodeSAP   byte = 5
	OpcodeGAP   byte = 6
	OpcodeSTAP  byte = 7
	OpcodeRSAP  byte = 8
	OpcodeSGP   byte = 9
	OpcodeGGP   byte = 10
	OpcodeSTGP  byte = 11
	OpcodeRSGP  byte = 12
	OpcodeRFS   byte = 13
	OpcodeSIO   byte = 14
	OpcodeGIO   byte = 15
	OpcodeCALC  byte = 19
	OpcodeCOMP  byte = 20
	OpcodeJC    byte = 21
	OpcodeJA    byte = 22
	OpcodeCSUB  byte = 23
	OpcodeRSUB  byte = 24
	OpcodeEI    byte = 25 // Enable Interrupt  – type = interrupt number
	OpcodeDI    byte = 26 // Disable Interrupt – type = interrupt number
	OpcodeWAIT  byte = 27
	OpcodeSTOP  byte = 28
	OpcodeRETI  byte = 38 // Return from Interrupt (no operands)
	OpcodeCALCX byte = 33
	OpcodeAAP   byte = 34 // Accumulator → Axis Parameter   – type=param, motor=motor
	OpcodeAGP   byte = 35 // Accumulator → Global Parameter – type=param, motor=bank
	OpcodeVECT  byte = 37 // Set Interrupt Vector – type=interrupt, value=address
)

// ProgramInstruction is a single compiled TMCL standalone instruction.
type ProgramInstruction struct {
	Cmd   byte
	Type  byte
	Motor byte
	Value int32
}

// DownloadProgram writes a compiled TMCL program into the module's EEPROM.
// A STOP instruction is appended automatically if the slice does not already
// end with one.
//
// The wire protocol exactly matches TMCL-IDE:
//
//	CMD 132 (START_DOWNLOAD)
//	  … one frame per instruction …
//	  NOP (0x00) — sentinel appended after the last instruction
//	CMD 133 (END_DOWNLOAD)
//
// The caller is responsible for calling RunProgram afterwards if the program
// should start immediately.
func (q *TMCL) DownloadProgram(instructions []ProgramInstruction) error {
	// Auto-append STOP so the board has a clean program terminator.
	if len(instructions) == 0 || instructions[len(instructions)-1].Cmd != OpcodeSTOP {
		instructions = append(instructions, ProgramInstruction{Cmd: OpcodeSTOP})
	}

	// Enter download mode (status 100 SUCCESS expected).
	if _, err := q.Exec(CmdStartDownload, 0, 0, 0); err != nil {
		return fmt.Errorf("start download: %w", err)
	}

	// Send each instruction; the board replies with status 101 ("loaded into
	// TMCL program EEPROM") which our Exec already treats as a nil error.
	for i, inst := range instructions {
		if _, err := q.Exec(inst.Cmd, inst.Type, inst.Motor, inst.Value); err != nil {
			return fmt.Errorf("instruction %d (cmd=0x%02X): %w", i, inst.Cmd, err)
		}
	}

	// TMCL-IDE always sends a trailing NOP(0) after the last instruction and
	// before END_DOWNLOAD (confirmed by USB pcap of a real TMCM-351 session).
	if _, err := q.Exec(OpcodeNOP, 0, 0, 0); err != nil {
		return fmt.Errorf("trailing nop: %w", err)
	}

	// Exit download mode (status 100 SUCCESS expected).
	if _, err := q.Exec(CmdEndDownload, 0, 0, 0); err != nil {
		return fmt.Errorf("end download: %w", err)
	}

	return nil
}

// DownloadAndRunProgram downloads the program and immediately starts it from
// the beginning. Equivalent to DownloadProgram + RunProgram.
func (q *TMCL) DownloadAndRunProgram(instructions []ProgramInstruction) error {
	if err := q.DownloadProgram(instructions); err != nil {
		return err
	}
	return q.RunProgram()
}

// RunProgram stops any currently running program, resets the program counter
// to zero, and starts execution from the beginning.
func (q *TMCL) RunProgram() error {
	if err := q.StopApplication(); err != nil {
		return fmt.Errorf("stop application: %w", err)
	}
	if err := q.ResetApplication(); err != nil {
		return fmt.Errorf("reset application: %w", err)
	}
	return q.RunApplication(false, 0)
}

//
// The TMCL-IDE "Write output to binary file" option produces a file where each
// instruction occupies exactly 8 bytes:
//
//	[0] cmd   [1] type   [2] motor   [3-6] value (big-endian)   [7] checksum
//
// Byte 7 is the sum of bytes 0–6 modulo 256 (for file integrity; it is not
// transmitted over the wire and is ignored by the loader).

// DownloadBinaryFile loads a TMCL binary (.bin) file exported by TMCL-IDE and
// downloads it to the board — no assembly step required.
func (q *TMCL) DownloadBinaryFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open binary file: %w", err)
	}
	defer f.Close()
	return q.DownloadBinary(f)
}

// DownloadAndRunBinaryFile loads, downloads, and runs a TMCL binary file.
func (q *TMCL) DownloadAndRunBinaryFile(path string) error {
	if err := q.DownloadBinaryFile(path); err != nil {
		return err
	}
	return q.RunProgram()
}

// DownloadBinary reads 8-byte instruction records from r and downloads them to
// the board.  The checksum byte (byte 7) is verified but not transmitted.
func (q *TMCL) DownloadBinary(r io.Reader) error {
	var instructions []ProgramInstruction

	var buf [8]byte
	for i := 0; ; i++ {
		_, err := io.ReadFull(r, buf[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read record %d: %w", i, err)
		}

		// Verify embedded checksum.
		var cs byte
		for _, b := range buf[:7] {
			cs += b
		}
		if cs != buf[7] {
			return fmt.Errorf("record %d: checksum mismatch (got 0x%02X, expected 0x%02X)", i, buf[7], cs)
		}

		value := int32(binary.BigEndian.Uint32(buf[3:7]))
		instructions = append(instructions, ProgramInstruction{
			Cmd:   buf[0],
			Type:  buf[1],
			Motor: buf[2],
			Value: value,
		})
	}

	return q.DownloadProgram(instructions)
}
