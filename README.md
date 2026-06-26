# go-tmcl

Go library for communicating with [Trinamic](https://www.trinamic.com/) TMCM stepper-motor boards over the TMCL (
Trinamic Motion Control Language) serial protocol.

> **Board compatibility:** Developed and tested against the **TMCM-351** (3-axis stepper controller).
> Other TMCM modules that implement the same TMCL instruction set should work but have not been verified.

---

## Packages

| Package                    | Description                                                                         |
|----------------------------|-------------------------------------------------------------------------------------|
| [`tmcl`](./tmcl)           | Core client — send commands, read/write axis & global parameters, download programs |
| [`assembler`](./assembler) | TMCL assembler — compile `.tmc` source files into binary programs                   |

---

## Installation

```bash
go get github.com/raceresult/go-tmcl/v3
```

---

## Quick start

### Connect and control a motor

```go
import "github.com/raceresult/go-tmcl/v3/tmcl"

board := tmcl.NewSerial()
if err := board.OpenPort("/dev/ttyUSB0", tmcl.DefaultSerialBaud); err != nil {
	log.Fatal(err)
}
defer board.ClosePort()

// Rotate right at velocity 200
if err := board.ROR(0, 200); err != nil {
	log.Fatal(err)
}

// Stop motor 0
board.MST(0)
```

### Compile and flash a standalone TMCL program

```go
import (
	"github.com/raceresult/go-tmcl/v3/assembler"
	"github.com/raceresult/go-tmcl/v3/tmcl"
)

instructions, err := assembler.CompileFile("program.tmc")
if err != nil {
	log.Fatal(err)
}

board := tmcl.NewSerial()
board.OpenPort("/dev/ttyUSB0", tmcl.DefaultSerialBaud)
defer board.ClosePort()

// Flash and immediately start the program
if err := board.DownloadAndRunProgram(instructions); err != nil {
	log.Fatal(err)
}
```

### Bring your own connection

`NewTMCL` accepts any `io.ReadWriter`, so you can wrap a TCP socket, a USB
HID device, or a test stub:

```go
conn, _ := net.Dial("tcp", "192.168.1.10:4001")
board := tmcl.NewTMCL(conn, tmcl.DefaultLogger{})
```

---

## Tools

`cmd/tmcl-flash` is a small CLI for compiling and flashing `.tmc` files:

```bash
go run ./cmd/tmcl-flash -port /dev/ttyUSB0 program.tmc
go run ./cmd/tmcl-flash -port /dev/ttyUSB0 -run program.tmc   # flash + start
```

---

## TMCL assembler

See [`assembler/README.md`](./assembler/README.md) for full documentation on the
assembler, the binary file format, and how to add your own test cases.
