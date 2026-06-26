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

// TestCheckCSUBDepth verifies that the assembler rejects programs that exceed
// the firmware's CSUB nesting limit of 8 and accepts programs that stay within it.
func TestCheckCSUBDepth(t *testing.T) {
	opts := assembler.Options{CheckCallDepth: true}

	// Helper: build a TMCL program with n levels of CSUB nesting.
	// Layout: each subroutine calls the next one, the innermost returns.
	//
	//   0: CSUB 2       ; call level-1 sub (entry at instruction 2)
	//   1: STOP
	//   2: CSUB 4       ; call level-2 sub (entry at instruction 4)
	//   3: RSUB
	//   4: CSUB 6       ; ...
	//   5: RSUB
	//   ...
	//   2*(n-1):   NOP  ; innermost subroutine body
	//   2*(n-1)+1: RSUB
	makeNested := func(n int) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			// Instruction index 2*i: call the next subroutine (starts at 2*(i+1))
			// or NOP for the innermost.
			if i < n-1 {
				fmt.Fprintf(&sb, "CSUB sub%d\n", i+1)
			} else {
				sb.WriteString("NOP\n")
			}
			// Instruction index 2*i+1: return (or STOP for the outermost caller).
			if i == 0 {
				sb.WriteString("STOP\n")
			} else {
				sb.WriteString("RSUB\n")
			}
			if i < n-1 {
				fmt.Fprintf(&sb, "sub%d:\n", i+1)
			}
		}
		return sb.String()
	}

	t.Run("depth8_ok", func(t *testing.T) {
		// makeNested(n) produces n-1 CSUB calls, so n=9 → depth 8 (the limit).
		src := makeNested(9)
		if _, err := assembler.Compile(src, opts); err != nil {
			t.Errorf("expected no error for depth 8, got: %v", err)
		}
	})

	t.Run("depth9_error", func(t *testing.T) {
		// n=10 → depth 9 (one beyond the limit).
		src := makeNested(10)
		_, err := assembler.Compile(src, opts)
		if err == nil {
			t.Error("expected error for depth 9, got nil")
		} else if !strings.Contains(err.Error(), "depth") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("recursion_error", func(t *testing.T) {
		// sub1 calls sub2, sub2 calls sub1 — mutual recursion.
		src := `
CSUB sub1
STOP
sub1:
CSUB sub2
RSUB
sub2:
CSUB sub1
RSUB
`
		_, err := assembler.Compile(src, opts)
		if err == nil {
			t.Error("expected error for recursive CSUB, got nil")
		} else if !strings.Contains(err.Error(), "recursive") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("testdata_depth9_rejected", func(t *testing.T) {
		_, err := assembler.CompileFileOpts("testdata/csub-depth9.tmc", opts)
		if err == nil {
			t.Error("expected assembler to reject csub-depth9.tmc, got nil error")
		} else {
			t.Logf("correctly rejected: %v", err)
		}
	})

	t.Run("testdata_depth8_accepted", func(t *testing.T) {
		if _, err := assembler.CompileFileOpts("testdata/csub-depth8.tmc", opts); err != nil {
			t.Errorf("expected assembler to accept csub-depth8.tmc, got: %v", err)
		}
	})

	t.Run("no_csub", func(t *testing.T) {
		src := "NOP\nSTOP\n"
		if _, err := assembler.Compile(src, opts); err != nil {
			t.Errorf("expected no error for program without CSUB, got: %v", err)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		// With CheckCallDepth disabled the depth-9 program must not produce an error.
		src := makeNested(10)
		if _, err := assembler.Compile(src, assembler.Options{}); err != nil {
			t.Errorf("expected no error when check is disabled, got: %v", err)
		}
	})
}

