// Package assembler provides a two-layer TMCL compilation pipeline.
//
// Layer 1 – core assembler: parses raw TMCL assembly source text and produces
// a []tmcl.ProgramInstruction ready to pass to a board's DownloadProgram.
//
// Layer 2 – preprocessor: extends the core assembler with #include directives
// and NAME = VALUE constant definitions, making it possible to compile
// real-world multi-file TMCL projects.
//
// Typical usage:
//
//	// Compile a source file to a binary file (offline, no board required):
//	err := assembler.WriteBinFile("out.bin", "program.tmc")
//
//	// Compile and flash to a connected board:
//	a := assembler.New(myTMCL)
//	err := a.CompileAndFlash("program.tmc")
package assembler

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/raceresult/go-tmcl/v3/tmcl"
)

type rangeError struct {
	lineNo int
	field  string
	value  int32
	min    int32
	max    int32
}

func (e *rangeError) Error() string {
	return fmt.Sprintf("line %d: %s value %d out of range [%d, %d]",
		e.lineNo, e.field, e.value, e.min, e.max)
}

func checkRange(lineNo int, field string, value, min, max int32) error {
	if value < min || value > max {
		return &rangeError{lineNo: lineNo, field: field, value: value, min: min, max: max}
	}
	return nil
}

// Options configures the full compilation pipeline (preprocessor + assembler).
type Options struct {
	// CheckRanges enables TMCM-351 parameter range validation.
	CheckRanges bool

	// AppendStop automatically adds a STOP instruction at the end of the
	// program if the last instruction is not already STOP.
	AppendStop bool

	// BaseDir is the directory used to resolve relative #include paths.
	// Defaults to the directory of the source file (for CompileFile) or the
	// current working directory (for Compile).
	BaseDir string
}

// DefaultOptions is the recommended default configuration: AppendStop enabled
// (matches TMCL-IDE default behaviour), no range checking.
var DefaultOptions = Options{AppendStop: true}

// JCCondition is the type field of the JC instruction.
type JCCondition byte

const (
	JCZero         JCCondition = 0  // ZE  – accumulator is zero
	JCNotZero      JCCondition = 1  // NZ  – accumulator is not zero
	JCEqual        JCCondition = 2  // EQ  – equal (same as ZE after COMP)
	JCNotEqual     JCCondition = 3  // NE  – not equal
	JCGreater      JCCondition = 4  // GT  – greater than
	JCGreaterEqual JCCondition = 5  // GE  – greater than or equal
	JCLess         JCCondition = 6  // LT  – less than
	JCLessEqual    JCCondition = 7  // LE  – less than or equal
	JCTimeout      JCCondition = 8  // ETO – timeout error
	JCAlarm        JCCondition = 9  // EAL – external alarm
	JCShutdown     JCCondition = 12 // ESD – shutdown error
)

// WAITCondition is the type field of the WAIT instruction.
type WAITCondition byte

const (
	WAITTicks WAITCondition = 0 // TICKS – wait a fixed number of 10 ms ticks
	WAITPos   WAITCondition = 1 // POS   – wait until target position reached
	WAITRefSW WAITCondition = 2 // REFSW – wait for reference switch
	WAITLimSW WAITCondition = 3 // LIMSW – wait for limit switch
	WAITRFS   WAITCondition = 4 // RFS   – wait for reference search to complete
)

// CalcOp is the type field of the CALC and CALCX instructions.
type CalcOp byte

const (
	CalcADD  CalcOp = 0 // ADD  – accu = accu + value
	CalcSUB  CalcOp = 1 // SUB  – accu = accu - value
	CalcMUL  CalcOp = 2 // MUL  – accu = accu * value
	CalcDIV  CalcOp = 3 // DIV  – accu = accu / value
	CalcMOD  CalcOp = 4 // MOD  – accu = accu mod value
	CalcAND  CalcOp = 5 // AND  – accu = accu & value
	CalcOR   CalcOp = 6 // OR   – accu = accu | value
	CalcXOR  CalcOp = 7 // XOR  – accu = accu ^ value
	CalcNOT  CalcOp = 8 // NOT  – accu = ~accu  (value ignored)
	CalcLOAD CalcOp = 9 // LOAD – accu = value
)

const (
	RFSStart  byte = 0 // START  – start reference search
	RFSStop   byte = 1 // STOP   – stop reference search
	RFSStatus byte = 2 // STATUS – query status
)

// assembleTMCProgramOpts parses TMCL assembly source text and returns the compiled
// instruction slice ready to pass to DownloadProgram.
func assembleTMCProgramOpts(source string, opts Options) ([]tmcl.ProgramInstruction, error) {
	lines := strings.Split(source, "\n")

	type rawLine struct {
		lineNo  int
		content string
	}

	labels := map[string]int{}
	var instrLines []rawLine

	for lineNo, line := range lines {
		line = stripComment(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasSuffix(line, ":") {
			name := strings.TrimSpace(strings.TrimSuffix(line, ":"))
			if name == "" {
				return nil, fmt.Errorf("line %d: empty label name", lineNo+1)
			}
			labels[strings.ToUpper(name)] = len(instrLines)
			continue
		}

		if idx := strings.Index(line, ":"); idx != -1 {
			candidate := strings.TrimSpace(line[:idx])
			if isIdentifier(candidate) {
				labels[strings.ToUpper(candidate)] = len(instrLines)
				line = strings.TrimSpace(line[idx+1:])
				if line == "" {
					continue
				}
			}
		}

		instrLines = append(instrLines, rawLine{lineNo: lineNo + 1, content: line})
	}

	var out []tmcl.ProgramInstruction

	resolveLabel := func(token string, lineNo int) (int32, error) {
		key := strings.ToUpper(strings.TrimSuffix(token, "%"))
		if addr, ok := labels[key]; ok {
			return int32(addr), nil
		}
		return 0, fmt.Errorf("line %d: undefined label %q", lineNo, token)
	}

	for _, rl := range instrLines {
		inst, err := assembleLine(rl.content, rl.lineNo, resolveLabel, opts.CheckRanges)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}

	if opts.AppendStop && (len(out) == 0 || out[len(out)-1].Cmd != tmcl.OpcodeSTOP) {
		out = append(out, tmcl.ProgramInstruction{Cmd: tmcl.OpcodeSTOP})
	}

	return out, nil
}

func assembleLine(
	line string,
	lineNo int,
	resolveLabel func(string, int) (int32, error),
	checkRanges bool,
) (tmcl.ProgramInstruction, error) {

	mnemonic, tokens, err := tokenizeLine(line, lineNo)
	if err != nil {
		return tmcl.ProgramInstruction{}, err
	}
	mnemonic = strings.ToUpper(mnemonic)

	parseInt := func(s string) (int32, error) {
		s = strings.TrimSpace(s)
		v, err := strconv.ParseInt(s, 0, 32)
		if err != nil {
			return 0, fmt.Errorf("line %d: expected integer, got %q", lineNo, s)
		}
		return int32(v), nil
	}
	need := func(n int) error {
		if len(tokens) != n {
			return fmt.Errorf("line %d: %s expects %d operand(s), got %d", lineNo, mnemonic, n, len(tokens))
		}
		return nil
	}
	needMin := func(n int) error {
		if len(tokens) < n {
			return fmt.Errorf("line %d: %s expects at least %d operand(s), got %d", lineNo, mnemonic, n, len(tokens))
		}
		return nil
	}

	switch mnemonic {
	case "NOP":
		if err := need(0); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeNOP}, nil

	case "STOP":
		if err := need(0); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeSTOP}, nil

	case "RSUB":
		if err := need(0); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeRSUB}, nil

	case "MST":
		if err := need(1); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		if checkRanges {
			if err := checkRange(lineNo, "motor", motor, 0, 2); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeMST, Motor: byte(motor)}, nil

	case "ROR":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		vel, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		if checkRanges {
			if err := checkRange(lineNo, "motor", motor, 0, 2); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
			if err := checkRange(lineNo, "velocity", vel, 0, 2047); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeROR, Motor: byte(motor), Value: vel}, nil

	case "ROL":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		vel, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		if checkRanges {
			if err := checkRange(lineNo, "motor", motor, 0, 2); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
			if err := checkRange(lineNo, "velocity", vel, 0, 2047); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeROL, Motor: byte(motor), Value: vel}, nil

	case "MVP":
		if err := needMin(3); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		modeCode, err := parseMVPMode(tokens[0], lineNo)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		val, err := parseInt(tokens[2])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		if checkRanges {
			if err := checkRange(lineNo, "motor", motor, 0, 2); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
			if modeCode == tmcl.COORD {
				if err := checkRange(lineNo, "coordinate number", val, 0, 20); err != nil {
					return tmcl.ProgramInstruction{}, err
				}
			}
		}
			return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeMVP, Type: byte(modeCode), Motor: byte(motor), Value: val}, nil

	case "SAP":
		if err := need(3); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		val, err := parseInt(tokens[2])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		if checkRanges {
			if err := checkRange(lineNo, "motor", motor, 0, 2); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
			if err := checkSAPRange(lineNo, param, val); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeSAP, Type: byte(param), Motor: byte(motor), Value: val}, nil

	case "GAP":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeGAP, Type: byte(param), Motor: byte(motor)}, nil

	case "STAP":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeSTAP, Type: byte(param), Motor: byte(motor)}, nil

	case "RSAP":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeRSAP, Type: byte(param), Motor: byte(motor)}, nil

	case "SGP":
		if err := need(3); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		bank, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		val, err := parseInt(tokens[2])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeSGP, Type: byte(param), Motor: byte(bank), Value: val}, nil

	case "GGP":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		bank, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeGGP, Type: byte(param), Motor: byte(bank)}, nil

	case "STGP":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		bank, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeSTGP, Type: byte(param), Motor: byte(bank)}, nil

	case "RSGP":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		bank, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeRSGP, Type: byte(param), Motor: byte(bank)}, nil

	case "SIO":
		if err := need(3); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		port, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		bank, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		val, err := parseInt(tokens[2])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		if checkRanges {
			if err := checkRange(lineNo, "SIO bank", bank, 0, 2); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
			if bank == 0 {
				if err := checkRange(lineNo, "SIO port (bank 0)", port, 0, 8); err != nil {
					return tmcl.ProgramInstruction{}, err
				}
				if err := checkRange(lineNo, "SIO value (bank 0)", val, 0, 1); err != nil {
					return tmcl.ProgramInstruction{}, err
				}
			} else if bank == 2 {
				if port != 255 {
					if err := checkRange(lineNo, "SIO port (bank 2)", port, 0, 7); err != nil {
						return tmcl.ProgramInstruction{}, err
					}
					if err := checkRange(lineNo, "SIO value (bank 2)", val, 0, 1); err != nil {
						return tmcl.ProgramInstruction{}, err
					}
				} else {
					if val != -1 {
						if err := checkRange(lineNo, "SIO value (bank 2, port 255)", val, 0, 255); err != nil {
							return tmcl.ProgramInstruction{}, err
						}
					}
				}
			}
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeSIO, Type: byte(port), Motor: byte(bank), Value: val}, nil

	case "GIO":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		port, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		bank, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		if checkRanges {
			if err := checkRange(lineNo, "GIO bank", bank, 0, 2); err != nil {
				return tmcl.ProgramInstruction{}, err
			}
			if port != 255 {
				if err := checkRange(lineNo, "GIO port", port, 0, 7); err != nil {
					return tmcl.ProgramInstruction{}, err
				}
			}
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeGIO, Type: byte(port), Motor: byte(bank)}, nil

	case "RFS":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		modeCode, err := parseRFSMode(tokens[0], lineNo)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeRFS, Type: modeCode, Motor: byte(motor)}, nil

	case "CALC":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		opCode, err := parseCalcOp(tokens[0], lineNo)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		val, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeCALC, Type: opCode, Value: val}, nil

	case "CALCX":
		if err := need(1); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		opCode, err := parseCalcOp(tokens[0], lineNo)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeCALCX, Type: opCode}, nil

	case "COMP":
		if err := need(1); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		val, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeCOMP, Value: val}, nil

	case "JC":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		cond, err := parseJCCondition(tokens[0], lineNo)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		addr, err := parseAddress(tokens[1], lineNo, resolveLabel, parseInt)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeJC, Type: cond, Value: addr}, nil

	case "JA":
		if err := need(1); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		addr, err := parseAddress(tokens[0], lineNo, resolveLabel, parseInt)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeJA, Value: addr}, nil

	case "CSUB":
		if err := need(1); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		addr, err := parseAddress(tokens[0], lineNo, resolveLabel, parseInt)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeCSUB, Value: addr}, nil

	case "WAIT":
		if err := need(3); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		cond, err := parseWAITCondition(tokens[0], lineNo)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		ticks, err := parseInt(tokens[2])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeWAIT, Type: cond, Motor: byte(motor), Value: ticks}, nil

	case "EI":
		if err := need(1); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		n, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeEI, Type: byte(n)}, nil

	case "DI":
		if err := need(1); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		n, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeDI, Type: byte(n)}, nil

	case "RETI":
		if err := need(0); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeRETI}, nil

	case "AAP":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		motor, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeAAP, Type: byte(param), Motor: byte(motor)}, nil

	case "AGP":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		param, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		bank, err := parseInt(tokens[1])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeAGP, Type: byte(param), Motor: byte(bank)}, nil

	case "VECT":
		if err := need(2); err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		n, err := parseInt(tokens[0])
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		addr, err := parseAddress(tokens[1], lineNo, resolveLabel, parseInt)
		if err != nil {
			return tmcl.ProgramInstruction{}, err
		}
		return tmcl.ProgramInstruction{Cmd: tmcl.OpcodeVECT, Type: byte(n), Value: addr}, nil

	default:
		return tmcl.ProgramInstruction{}, fmt.Errorf("line %d: unknown instruction %q", lineNo, mnemonic)
	}
}

func parseMVPMode(s string, lineNo int) (tmcl.MVPMode, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ABS":
		return tmcl.ABS, nil
	case "REL":
		return tmcl.REL, nil
	case "COORD":
		return tmcl.COORD, nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 0, 8)
	if err != nil {
		return 0, fmt.Errorf("line %d: unknown MVP mode %q (use ABS, REL or COORD)", lineNo, s)
	}
	return tmcl.MVPMode(v), nil
}

func parseRFSMode(s string, lineNo int) (byte, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "START":
		return RFSStart, nil
	case "STOP":
		return RFSStop, nil
	case "STATUS":
		return RFSStatus, nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 0, 8)
	if err != nil {
		return 0, fmt.Errorf("line %d: unknown RFS mode %q (use START, STOP or STATUS)", lineNo, s)
	}
	return byte(v), nil
}

func parseCalcOp(s string, lineNo int) (byte, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ADD":
		return byte(CalcADD), nil
	case "SUB":
		return byte(CalcSUB), nil
	case "MUL":
		return byte(CalcMUL), nil
	case "DIV":
		return byte(CalcDIV), nil
	case "MOD":
		return byte(CalcMOD), nil
	case "AND":
		return byte(CalcAND), nil
	case "OR":
		return byte(CalcOR), nil
	case "XOR":
		return byte(CalcXOR), nil
	case "NOT":
		return byte(CalcNOT), nil
	case "LOAD":
		return byte(CalcLOAD), nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 0, 8)
	if err != nil {
		return 0, fmt.Errorf("line %d: unknown CALC operation %q", lineNo, s)
	}
	return byte(v), nil
}

func parseJCCondition(s string, lineNo int) (byte, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ZE":
		return byte(JCZero), nil
	case "NZ":
		return byte(JCNotZero), nil
	case "EQ":
		return byte(JCEqual), nil
	case "NE":
		return byte(JCNotEqual), nil
	case "GT":
		return byte(JCGreater), nil
	case "GE":
		return byte(JCGreaterEqual), nil
	case "LT":
		return byte(JCLess), nil
	case "LE":
		return byte(JCLessEqual), nil
	case "ETO":
		return byte(JCTimeout), nil
	case "EAL":
		return byte(JCAlarm), nil
	case "ESD":
		return byte(JCShutdown), nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 0, 8)
	if err != nil {
		return 0, fmt.Errorf("line %d: unknown JC condition %q (use ZE,NZ,EQ,NE,GT,GE,LT,LE,ETO,EAL,ESD)", lineNo, s)
	}
	return byte(v), nil
}

func parseWAITCondition(s string, lineNo int) (byte, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TICKS":
		return byte(WAITTicks), nil
	case "POS":
		return byte(WAITPos), nil
	case "REFSW":
		return byte(WAITRefSW), nil
	case "LIMSW":
		return byte(WAITLimSW), nil
	case "RFS":
		return byte(WAITRFS), nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 0, 8)
	if err != nil {
		return 0, fmt.Errorf("line %d: unknown WAIT condition %q (use TICKS,POS,REFSW,LIMSW,RFS)", lineNo, s)
	}
	return byte(v), nil
}

func parseAddress(
	token string,
	lineNo int,
	resolveLabel func(string, int) (int32, error),
	parseInt func(string) (int32, error),
) (int32, error) {
	token = strings.TrimSpace(token)
	if isNumericToken(token) {
		return parseInt(token)
	}
	return resolveLabel(token, lineNo)
}

func tokenizeLine(line string, lineNo int) (mnemonic string, tokens []string, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", nil, fmt.Errorf("line %d: empty instruction", lineNo)
	}
	idx := strings.IndexAny(line, " \t")
	if idx == -1 {
		return line, nil, nil
	}
	mnemonic = line[:idx]
	rest := strings.TrimSpace(line[idx+1:])
	if rest == "" {
		return mnemonic, nil, nil
	}
	raw := strings.Split(rest, ",")
	for _, op := range raw {
		op = strings.TrimSpace(op)
		if op != "" {
			tokens = append(tokens, op)
		}
	}
	return mnemonic, tokens, nil
}

func checkSAPRange(lineNo int, param, value int32) error {
	type paramRange struct{ min, max int32 }
	ranges := map[int32]paramRange{
		0:   {-1 << 31, 1<<31 - 1},
		1:   {-1 << 31, 1<<31 - 1},
		2:   {-2047, 2047},
		4:   {0, 2047},
		5:   {0, 2047},
		6:   {0, 255},
		7:   {0, 255},
		12:  {0, 1},
		13:  {0, 1},
		130: {0, 2047},
		138: {0, 2},
		140: {0, 6},
		141: {0, 4095},
		149: {0, 1},
		153: {0, 13},
		154: {0, 13},
		193: {1, 3},
		194: {0, 2047},
		195: {0, 2047},
		200: {0, 255},
		203: {-1, 2048},
		204: {0, 65535},
		205: {0, 7},
		210: {0, 65535},
		211: {0, 2048},
		212: {0, 65535},
		213: {0, 255},
		214: {1, 65535},
	}
	if r, ok := ranges[param]; ok {
		return checkRange(lineNo, fmt.Sprintf("SAP[%d] value", param), value, r.min, r.max)
	}
	return nil
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_'
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	// First character must be a letter or underscore (not a digit).
	if !(s[0] == '_' || (s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentByte(s[i]) {
			return false
		}
	}
	return true
}

func isNumericToken(s string) bool {
	if s == "" {
		return false
	}
	return (s[0] >= '0' && s[0] <= '9') || s[0] == '-' || s[0] == '+'
}

func stripComment(line string) string {
	code, _ := splitLineComment(line)
	return code
}

// Compile preprocesses and assembles TMCL source text.
func Compile(source string, opts Options) ([]tmcl.ProgramInstruction, error) {
	baseDir := opts.BaseDir
	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			baseDir = "."
		}
	}

	preprocessed, err := preprocess(source, baseDir, make(map[string]string))
	if err != nil {
		return nil, err
	}

	return assembleTMCProgramOpts(preprocessed, opts)
}

// CompileFile reads a .tmc source file, preprocesses it, and assembles it.
// Uses DefaultOptions (AppendStop: true, CheckRanges: false).
func CompileFile(path string) ([]tmcl.ProgramInstruction, error) {
	return CompileFileOpts(path, DefaultOptions)
}

// CompileFileOpts is like CompileFile but with explicit options.
func CompileFileOpts(path string, opts Options) ([]tmcl.ProgramInstruction, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if opts.BaseDir == "" {
		opts.BaseDir = filepath.Dir(path)
	}
	return Compile(string(src), opts)
}

// WriteBinFile compiles sourcePath and writes the resulting binary to
// outputPath in the TMCL-IDE 8-bytes-per-instruction format.
func WriteBinFile(outputPath, sourcePath string) error {
	instructions, err := CompileFile(sourcePath)
	if err != nil {
		return err
	}
	return WriteBinaryFile(outputPath, instructions)
}

// WriteBinaryFile encodes instructions in the TMCL-IDE binary format and
// writes them to path.  This lets you pre-compile programs offline and later
// flash them without an assembly step (via board.DownloadBinaryFile).
//
// The binary format per instruction:
//
//	[0] cmd  [1] type  [2] motor  [3-6] value (big-endian)  [7] checksum
func WriteBinaryFile(path string, instructions []tmcl.ProgramInstruction) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create binary file: %w", err)
	}
	defer f.Close()
	return WriteBinary(f, instructions)
}

// WriteBinary encodes instructions in the 8-byte-per-record TMCL binary format
// and writes them to w.
func WriteBinary(w io.Writer, instructions []tmcl.ProgramInstruction) error {
	var buf [8]byte
	for i, inst := range instructions {
		buf[0] = inst.Cmd
		buf[1] = inst.Type
		buf[2] = inst.Motor
		binary.BigEndian.PutUint32(buf[3:7], uint32(inst.Value))
		var cs byte
		for _, b := range buf[:7] {
			cs += b
		}
		buf[7] = cs
		if _, err := w.Write(buf[:]); err != nil {
			return fmt.Errorf("write record %d: %w", i, err)
		}
	}
	return nil
}

func preprocess(source, baseDir string, constants map[string]string) (string, error) {
	var result strings.Builder
	lines := strings.Split(source, "\n")

	for _, line := range lines {
		code, comment := splitLineComment(line)
		trimmed := strings.TrimSpace(code)

		if strings.HasPrefix(trimmed, "#include") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "#include"))
			rest = strings.Trim(rest, "\"<>")
			includePath := filepath.Join(baseDir, rest)
			content, err := os.ReadFile(includePath)
			if err != nil {
				return "", fmt.Errorf("#include %q: %w", rest, err)
			}
			included, err := preprocess(string(content), filepath.Dir(includePath), constants)
			if err != nil {
				return "", fmt.Errorf("#include %q: %w", rest, err)
			}
			result.WriteString(included)
			continue
		}

		if eqIdx := strings.Index(trimmed, "="); eqIdx > 0 {
			lhs := strings.TrimSpace(trimmed[:eqIdx])
			rhs := strings.TrimSpace(trimmed[eqIdx+1:])
			if isIdentifier(lhs) && rhs != "" {
				resolved := resolveConstant(rhs, constants)
				constants[strings.ToUpper(lhs)] = resolved
				continue
			}
		}

		substituted := replaceTokens(code, constants)
		result.WriteString(substituted)
		result.WriteString(comment)
		result.WriteString("\n")
	}

	return result.String(), nil
}

func splitLineComment(line string) (code, comment string) {
	commentStart := -1
	if i := strings.Index(line, "//"); i >= 0 {
		commentStart = i
	}
	if i := strings.Index(line, ";"); i >= 0 && (commentStart < 0 || i < commentStart) {
		commentStart = i
	}
	if commentStart < 0 {
		return line, ""
	}
	return line[:commentStart], line[commentStart:]
}

func replaceTokens(s string, constants map[string]string) string {
	if len(constants) == 0 {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	i := 0
	for i < len(s) {
		c := s[i]
		if isIdentByte(c) {
			j := i
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			token := s[i:j]
			if j < len(s) && s[j] == ':' {
				out.WriteString(token)
			} else if val, ok := constants[strings.ToUpper(token)]; ok {
				out.WriteString(val)
			} else {
				out.WriteString(token)
			}
			i = j
		} else {
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

func resolveConstant(rhs string, constants map[string]string) string {
	upper := strings.ToUpper(strings.TrimSpace(rhs))
	if val, ok := constants[upper]; ok {
		return val
	}
	return strings.TrimSpace(rhs)
}
