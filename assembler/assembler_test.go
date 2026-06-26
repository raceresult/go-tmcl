package assembler_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raceresult/go-tmcl/v3/assembler"
)

// TestAssembleMatchesTMCLIDE compiles every .tmc file in testdata/ that has a
// corresponding .bin file and verifies that the binary output is byte-for-byte
// identical to the TMCL-IDE reference binary.
func TestAssembleMatchesTMCLIDE(t *testing.T) {
	tmcFiles, err := filepath.Glob("testdata/*.tmc")
	if err != nil {
		t.Fatalf("glob testdata/*.tmc: %v", err)
	}

	tested := 0
	for _, tmcPath := range tmcFiles {
		binPath := strings.TrimSuffix(tmcPath, ".tmc") + ".bin"
		if _, err := os.Stat(binPath); os.IsNotExist(err) {
			// No reference binary — skip (e.g. Constants.tmc is an include-only file).
			t.Logf("skip %s (no matching .bin)", filepath.Base(tmcPath))
			continue
		}

		name := filepath.Base(strings.TrimSuffix(tmcPath, ".tmc"))
		t.Run(name, func(t *testing.T) {
			// Compile the source file.
			instructions, err := assembler.CompileFile(tmcPath)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			// Encode the instructions to the TMCL-IDE 8-byte binary format.
			var got bytes.Buffer
			if err := assembler.WriteBinary(&got, instructions); err != nil {
				t.Fatalf("encode binary: %v", err)
			}

			// Load the reference binary produced by TMCL-IDE.
			want, err := os.ReadFile(binPath)
			if err != nil {
				t.Fatalf("read reference binary %s: %v", binPath, err)
			}

			// Byte-for-byte comparison.
			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("binary output mismatch for %s", name)
				t.Errorf("  got  %d bytes, want %d bytes", got.Len(), len(want))
				diffInstructions(t, got.Bytes(), want)
			}
		})
		tested++
	}

	if tested == 0 {
		t.Fatal("no .tmc/.bin pairs found in testdata/")
	}
}

// diffInstructions prints a side-by-side diff at instruction granularity
// (8 bytes per instruction) to make failures easy to diagnose.
func diffInstructions(t *testing.T, got, want []byte) {
	t.Helper()

	maxLen := len(got)
	if len(want) > maxLen {
		maxLen = len(want)
	}

	for i := 0; i < maxLen; i += 8 {
		gChunk := chunkAt(got, i)
		wChunk := chunkAt(want, i)
		if !bytes.Equal(gChunk, wChunk) {
			t.Errorf("  instruction #%d:", i/8)
			if gChunk != nil {
				t.Errorf("    got : cmd=%d type=%d motor=%d value=%d  (%s)",
					gChunk[0], gChunk[1], gChunk[2],
					int32(binary.BigEndian.Uint32(gChunk[3:7])),
					fmt.Sprintf("%x", gChunk))
			} else {
				t.Errorf("    got : (missing)")
			}
			if wChunk != nil {
				t.Errorf("    want: cmd=%d type=%d motor=%d value=%d  (%s)",
					wChunk[0], wChunk[1], wChunk[2],
					int32(binary.BigEndian.Uint32(wChunk[3:7])),
					fmt.Sprintf("%x", wChunk))
			} else {
				t.Errorf("    want: (missing)")
			}
		}
	}
}

func chunkAt(b []byte, i int) []byte {
	if i >= len(b) {
		return nil
	}
	end := i + 8
	if end > len(b) {
		end = len(b)
	}
	return b[i:end]
}
