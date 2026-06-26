// tmcl-flash compiles a TMCL source file and flashes it to a connected board
// over a serial port.
//
// Usage:
//
//	tmcl-flash [flags] <source.tmc>
//
// Flags:
//
//	-port  string   Serial port to use (default "/dev/ttyUSB0")
//	-baud  int      Baud rate (default 9600)
//	-run           Also start the program after flashing (default: false)
//
// Example:
//
//	tmcl-flash -port /dev/ttyUSB0 -baud 9600 assembler/testdata/hello-world.tmc
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/raceresult/go-tmcl/v3/assembler"
	"github.com/raceresult/go-tmcl/v3/tmcl"
)

func main() {
	port := flag.String("port", "/dev/ttyACM0", "serial port (e.g. /dev/ttyUSB0 or COM3)")
	baud := flag.Int("baud", 9600, "baud rate")
	run := flag.Bool("run", false, "start the program on the board after flashing")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: tmcl-flash [flags] <source.tmc>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	sourceFile := flag.Arg(0)

	// Open the serial connection.
	board := tmcl.NewSerial()
	if err := board.OpenPort(*port, *baud); err != nil {
		log.Fatalf("open port %s @ %d baud: %v", *port, *baud, err)
	}
	defer board.ClosePort()

	log.Printf("connected to %s @ %d baud", *port, *baud)

	// Print firmware version so we know we're talking to the right thing.
	ver, err := board.GetFirmwareVersion()
	if err != nil {
		log.Fatalf("get firmware version: %v", err)
	}
	log.Printf("firmware version: %s", ver)

	// Compile the source file.
	log.Printf("compiling %s …", sourceFile)
	instructions, err := assembler.CompileFile(sourceFile)
	if err != nil {
		log.Fatalf("compile: %v", err)
	}
	log.Printf("compiled %d instruction(s)", len(instructions))

	// Stop all motors before flashing — avoids running motors while the
	// EEPROM is being written.
	log.Printf("stopping motors …")
	if err := board.StopMotors(3); err != nil {
		log.Fatalf("stop motors: %v", err)
	}

	// Flash the program to the board's EEPROM.
	log.Printf("flashing …")
	if err := board.DownloadProgram(instructions); err != nil {
		log.Fatalf("flash: %v", err)
	}
	log.Println("program flashed to board ✓")

	// Optionally start the program immediately.
	if *run {
		if err := board.RunProgram(); err != nil {
			log.Fatalf("run: %v", err)
		}
		log.Println("program started on board ✓")
	} else {
		log.Println("(use -run to start it automatically)")
	}
}
