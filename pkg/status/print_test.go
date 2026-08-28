package status

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/korthane/rrev/pkg/processor/phase"
)

func TestPrinterAttributesEachPhase(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, false)

	fmt.Fprintln(p.Stream(phase.NameComprehensive), "reading the diff")
	fmt.Fprintln(p.Stream(phase.NameExternal), "codex says nothing")
	p.Say("comprehensive review ended after 1 iteration: converged")

	want := "[comprehensive] reading the diff\n[external] codex says nothing\n" +
		"comprehensive review ended after 1 iteration: converged\n"
	if buf.String() != want {
		t.Errorf("output =\n%q\nwant\n%q", buf.String(), want)
	}
}

// TestPrinterSplitsPartialWrites covers a tool that delivers a line in pieces:
// the prefix belongs once per line, not once per write.
func TestPrinterSplitsPartialWrites(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, false)
	stream := p.Stream(phase.NameFinal)

	for _, chunk := range []string{"one ", "line\ntwo ", "lines\nand a tail"} {
		if _, err := stream.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got, want := buf.String(), "[final] one line\n[final] two lines\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}

	p.Flush()
	if want := "[final] and a tail\n"; !strings.HasSuffix(buf.String(), want) {
		t.Errorf("the trailing text was never flushed: %q", buf.String())
	}
}

func TestPrinterColors(t *testing.T) {
	var colored, plain bytes.Buffer
	fmt.Fprintln(New(&colored, true).Stream(phase.NameComprehensive), "hello")
	fmt.Fprintln(New(&plain, false).Stream(phase.NameComprehensive), "hello")

	if !strings.Contains(colored.String(), ansiCyan) || !strings.Contains(colored.String(), ansiReset) {
		t.Errorf("coloured output = %q, want it wrapped in the phase colour", colored.String())
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Errorf("plain output = %q, want no escape sequences", plain.String())
	}
}

// TestPrinterConcurrentPhases guards the lock the comprehensive phase needs: its
// reviewers write at once and their lines must not interleave mid-line.
func TestPrinterConcurrentPhases(t *testing.T) {
	var buf bytes.Buffer
	p := New(&buf, true)

	var wg sync.WaitGroup
	for _, name := range []string{phase.NameComprehensive, phase.NameExternal, phase.NameFinal} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream := p.Stream(name)
			for i := range 50 {
				fmt.Fprintf(stream, "%s line %d\n", name, i)
			}
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 150 {
		t.Fatalf("got %d lines, want 150", len(lines))
	}
	for _, line := range lines {
		if strings.Count(line, "line") != 1 {
			t.Fatalf("interleaved line: %q", line)
		}
	}
}

func TestUseColor(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")

	if UseColor(&bytes.Buffer{}, false) {
		t.Error("a buffer is not a terminal, so colour must be off")
	}
	if UseColor(&bytes.Buffer{}, true) {
		t.Error("no_color must turn colour off")
	}

	t.Setenv("NO_COLOR", "1")
	if UseColor(&bytes.Buffer{}, false) {
		t.Error("NO_COLOR must turn colour off")
	}
}
