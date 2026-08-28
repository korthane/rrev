package status

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/korthane/rrev/pkg/processor/phase"
)

// ANSI attributes. The palette stays inside the eight basic colours, which
// every terminal capable of colour at all renders correctly.
const (
	ansiReset   = "\x1b[0m"
	ansiCyan    = "\x1b[36m"
	ansiMagenta = "\x1b[35m"
	ansiYellow  = "\x1b[33m"
	ansiGreen   = "\x1b[32m"
	ansiBlue    = "\x1b[34m"
)

// phaseColors attributes each phase a stable colour, so interleaved output from
// two phases stays tellable apart at a glance.
var phaseColors = map[string]string{
	phase.NameComprehensive: ansiCyan,
	phase.NameExternal:      ansiMagenta,
	phase.NameFinal:         ansiYellow,
	phase.NameFinalize:      ansiGreen,
}

// Printer writes rrev's output, attributing executor activity to the phase that
// produced it. It is safe for concurrent use: the comprehensive phase runs its
// reviewers at once and their lines must not interleave mid-line.
type Printer struct {
	out   io.Writer
	color bool

	mu      sync.Mutex
	writers []*prefixWriter
}

// New builds a printer writing to out. A nil out discards everything.
func New(out io.Writer, color bool) *Printer {
	if out == nil {
		out = io.Discard
	}
	return &Printer{out: out, color: color}
}

// Plain returns the writer rrev's own narration goes to. Those lines name their
// phase in words already, so they carry no prefix.
func (p *Printer) Plain() io.Writer { return p.writer("") }

// Stream returns where one phase's executor activity is written, each line
// prefixed with the phase it came from.
func (p *Printer) Stream(name string) io.Writer { return p.writer(name) }

// Say writes one already-complete line of narration.
func (p *Printer) Say(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeLine("", fmt.Sprintf(format, args...))
}

// Flush emits the trailing text of any stream that ended without a newline, so
// a tool's last words are not swallowed by the buffer that assembles lines.
func (p *Printer) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.writers {
		if len(w.buf) > 0 {
			p.writeLine(w.label, string(w.buf))
			w.buf = w.buf[:0]
		}
	}
}

func (p *Printer) writer(label string) io.Writer {
	w := &prefixWriter{printer: p, label: label}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writers = append(p.writers, w)
	return w
}

// writeLine renders one line. The caller holds the lock.
func (p *Printer) writeLine(label, line string) {
	prefix := ""
	if label != "" {
		prefix = "[" + label + "] "
		if p.color {
			prefix = colorFor(label) + prefix + ansiReset
		}
	}
	// A terminal that cannot be written to is not worth failing a review for.
	_, _ = fmt.Fprintln(p.out, prefix+strings.TrimRight(line, "\r"))
}

func colorFor(label string) string {
	if c, ok := phaseColors[label]; ok {
		return c
	}
	return ansiBlue
}

// prefixWriter turns a stream of bytes into whole lines, since a prefix can
// only be written once per line and a tool may deliver one in several writes.
type prefixWriter struct {
	printer *Printer
	label   string
	buf     []byte
}

func (w *prefixWriter) Write(b []byte) (int, error) {
	w.printer.mu.Lock()
	defer w.printer.mu.Unlock()
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.printer.writeLine(w.label, string(w.buf[:i]))
		w.buf = append(w.buf[:0], w.buf[i+1:]...)
	}
	return len(b), nil
}

// UseColor decides whether coloured output suits out: never when it was turned
// off, never for something that is not a terminal, and never under a non-empty
// NO_COLOR, which is the cross-tool convention for the same request.
func UseColor(out io.Writer, noColor bool) bool {
	if noColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
