# assembler

Go package for compiling TMCL (Trinamic Motion Control Language) assembly source
files (`.tmc`) into binary programs that can be flashed to a TMCM board.

> **Board compatibility:** This assembler has been tested against the **TMCM-351**
> firmware. Other TMCM modules that use the same TMCL instruction set should work,
> but have not been verified.

---

## Binary format

Each compiled instruction is encoded as exactly **8 bytes** in the format used by
TMCL-IDE's *"Write output to binary file"* option:

```
Byte  0      cmd      – instruction opcode
Byte  1      type     – type / parameter number
Byte  2      motor    – motor / bank number
Bytes 3–6    value    – 32-bit signed integer, big-endian
Byte  7      checksum – sum of bytes 0–6, modulo 256
```

The checksum is a simple integrity guard for the file itself; it is **not**
transmitted over the wire when downloading a program to the board.

A complete program is the concatenation of all instruction records with no header
or trailer. The file size is always a multiple of 8 bytes.

### Example — `hello-world.bin` (12 instructions × 8 bytes = 96 bytes)

```
Offset  Cmd  Type Motor Value (BE)   Cksum  Meaning
------  ---  ---- ----- -----------  -----  -------
0x00    05   06   00    00 00 00 C8   D3     SAP 6, 0, 200
0x08    05   05   00    00 00 00 64   6E     SAP 5, 0, 100
0x10    01   00   00    00 00 00 32   33     ROR 0, 50       ← LOOP label
0x18    1B   00   00    00 00 01 F4   10     WAIT TICKS, 0, 500
0x20    03   00   00    00 00 00 00   03     MST 0
0x28    1B   00   00    00 00 01 F4   10     WAIT TICKS, 0, 500
0x30    02   00   00    00 00 00 32   34     ROL 0, 50
0x38    1B   00   00    00 00 01 F4   10     WAIT TICKS, 0, 500
0x40    03   00   00    00 00 00 00   03     MST 0
0x48    1B   00   00    00 00 01 F4   10     WAIT TICKS, 0, 500
0x50    16   00   00    00 00 00 02   18     JA 2            ← jump back to LOOP
0x58    1C   00   00    00 00 00 00   1C     STOP
```

---

## Usage

```go
import (
"github.com/raceresult/go-tmcl/v3/assembler"
"github.com/raceresult/go-tmcl/v3/tmcl"
)

// Compile offline → binary file (no board required)
err := assembler.WriteBinFile("out.bin", "program.tmc")

// Compile and flash to a connected board
board := tmcl.NewSerial()
board.OpenPort("/dev/ttyUSB0", 9600)
instructions, err := assembler.CompileFile("program.tmc")
err = board.DownloadProgram(instructions) // flash only
err = board.DownloadAndRunProgram(instructions) // flash + start
```

### Preprocessor features

The assembler understands two preprocessing directives on top of raw TMCL syntax:

| Feature        | Syntax                                 | Example                  |
|----------------|----------------------------------------|--------------------------|
| Include file   | `#include <file>` or `#include "file"` | `#include Constants.tmc` |
| Named constant | `NAME = VALUE`                         | `MOTOR_BELT = 0`         |

Constant names are case-insensitive and are substituted in instruction operands.
Relative paths are resolved relative to the directory of the source file.

---

## Adding your own tests

The `testdata/` directory is picked up automatically by `go test`. To verify
that the assembler produces output identical to TMCL-IDE for your own programs:

1. **Get the reference binary from TMCL-IDE:**
    1. Open TMCL-IDE and click the **Eye icon** in the top-right toolbar to open a
       *Simulated TMCM Board* (no physical hardware required).
    2. Open **TMCL → TMCL-Creator** and load or write your program.
    3. Before compiling, enable **TMCL → Options → Write output to binary file**.
    4. Compile the program. TMCL-IDE writes a `.bin` file next to your source.

2. **Place both files in `testdata/`:**

   ```
   assembler/testdata/
       MyProgram.tmc   ← your TMCL source
       MyProgram.bin   ← reference binary from TMCL-IDE
       Constants.tmc   ← shared include files (no .bin needed)
   ```

   The file stem must match exactly (`MyProgram.tmc` ↔ `MyProgram.bin`).

3. **Run the tests:**

   ```bash
   go test ./assembler/
   ```

   Any `.tmc` file without a matching `.bin` is silently skipped (useful for
   shared include-only files like `Constants.tmc`).

On failure the test prints a per-instruction diff showing the differing opcode,
type, motor, and value fields alongside their hex bytes, making it easy to
pinpoint the first divergence.

