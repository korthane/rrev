package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/status"
)

// phaseLines identifies which phase a prompt belongs to by the opening line
// each default prompt carries. It is what makes the phase sequence observable
// from outside the process: the stand-in executors record what they were sent.
var phaseLines = []struct{ line, name string }{
	{"Comprehensive review of:", "comprehensive"},
	{"Independent review of:", "external"},
	{"External review evaluation for:", "external-eval"},
	{"Final regression review of:", "final"},
	{"Finalize step for:", "finalize"},
}

// standInScript is a scripted AI executor. It identifies the phase from the
// prompt on stdin, records the invocation, and runs the case body a test
// supplied. The body dispatches on "$phase:$n" and may call `commit` and
// `requirement`. PATH is the fake bin directory alone, so the script puts the
// system directories back for the utilities it needs.
const standInScript = `#!/bin/sh
PATH="$PATH:/usr/bin:/bin"
run=%q
name=%q
prompt="$run/prompt.$$"
cat > "$prompt"
phase=unknown
%s
printf '%%s:%%s\n' "$name" "$phase" >> "$run/sequence"
n=$(grep -c ":$phase$" "$run/sequence")
requirement() { sed -n "s/^$1\. \[[A-Z]*\] //p" "$prompt" | head -1; }
commit() { git add -A >/dev/null && git commit -q --allow-empty -m "$1" >/dev/null; }
%s
exit 0
`

// scriptedRun is the record a set of stand-in executors leaves behind.
type scriptedRun struct{ dir string }

// scriptExecutors replaces PATH with a stand-in per named tool, each running
// the shell case body given for it.
func scriptExecutors(t *testing.T, bodies map[string]string) *scriptedRun {
	t.Helper()
	names := slices.Sorted(maps.Keys(bodies))
	dir := filepath.Dir(fakeBin(t, names...))

	var detect strings.Builder
	for _, p := range phaseLines {
		fmt.Fprintf(&detect, "grep -q %q \"$prompt\" && phase=%s\n", p.line, p.name)
	}
	for _, name := range names {
		script := fmt.Sprintf(standInScript, dir, name, detect.String(), bodies[name])
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { //nolint:gosec // test helper
			t.Fatalf("write %s stand-in: %v", name, err)
		}
	}
	return &scriptedRun{dir: dir}
}

// sequence lists every executor call as `tool:phase`, in the order they ran.
func (r *scriptedRun) sequence(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(r.dir, "sequence")) //nolint:gosec // the path is a test temp file
	if err != nil {
		return nil
	}
	return strings.Fields(string(body))
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// TestEndToEndFullPipeline drives a whole default run through scripted
// executors: the comprehensive phase iterates once before converging, the
// external loop alternates codex with the primary executor, and the final
// regression pass runs because fixes were committed along the way.
func TestEndToEndFullPipeline(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	script := scriptExecutors(t, map[string]string{
		"claude": `case "$phase:$n" in
  comprehensive:1)
    echo "FINDING: major | auth.go:1 | quality | - | the handler creates no session"
    commit "fix the sign-in handler"
    ;;
  comprehensive:2) echo "<<<RREV:REVIEW_DONE>>>" ;;
  external-eval:1)
    echo "FINDING: minor | auth.go:2 | external | - | the error path is unhandled"
    echo "REJECTED: auth.go:9 | external | the cited line does not exist"
    commit "address the external review"
    echo "<<<RREV:EXTERNAL_DONE>>>"
    ;;
  final:1) echo "<<<RREV:REVIEW_DONE>>>" ;;
  *) echo "<<<RREV:TASK_FAILED>>>" ;;
esac`,
		"codex": `case "$phase:$n" in
  external:1) echo "FINDING: minor | auth.go:2 | external | - | the error path is unhandled" ;;
  *) echo "<<<RREV:EXTERNAL_DONE>>>" ;;
esac`,
	})
	t.Chdir(repo)

	var out strings.Builder
	code := run(context.Background(), nil, &out, io.Discard)
	if code != status.CodeOK {
		t.Fatalf("code = %d, want %d; output:\n%s", code, status.CodeOK, out.String())
	}

	want := []string{
		"claude:comprehensive", "claude:comprehensive",
		"codex:external", "claude:external-eval",
		"claude:final",
	}
	if got := script.sequence(t); !slices.Equal(got, want) {
		t.Errorf("phase sequence = %v, want %v; output:\n%s", got, want, out.String())
	}

	log := gitOutput(t, repo, "log", "main..HEAD", "--format=%s")
	for _, subject := range []string{"fix the sign-in handler", "address the external review"} {
		if !strings.Contains(log, subject) {
			t.Errorf("commit %q is missing from the branch:\n%s", subject, log)
		}
	}
	if got, want := len(strings.Split(log, "\n")), 3; got != want {
		t.Errorf("branch has %d commits, want %d:\n%s", got, want, log)
	}
	if !strings.Contains(out.String(), "run converged with nothing outstanding") {
		t.Errorf("a converged run must say so; output:\n%s", out.String())
	}
	assertLogged(t, repo, "the cited line does not exist")
}

// TestEndToEndReportOnly covers the mode that exists to be safe: the reviewers
// run, a report is written, and the repository is exactly as it was found.
func TestEndToEndReportOnly(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	script := scriptExecutors(t, map[string]string{
		"claude": `case "$phase:$n" in
  comprehensive:1)
    echo "FINDING: major | auth.go:1 | quality | - | the handler creates no session"
    ;;
  external-eval:1)
    echo "FINDING: minor | auth.go:2 | external | - | the error path is unhandled"
    ;;
esac`,
		"codex": `case "$phase:$n" in
  external:1) echo "FINDING: minor | auth.go:2 | external | - | the error path is unhandled" ;;
esac`,
	})
	t.Chdir(repo)

	head := gitOutput(t, repo, "rev-parse", "HEAD")

	var out strings.Builder
	// Findings left outstanding are what a read-only run reports, so it does
	// not exit clean.
	if code := run(context.Background(), []string{"--report-only"}, &out, io.Discard); code != status.CodeUnconverged {
		t.Fatalf("code = %d, want %d; output:\n%s", code, status.CodeUnconverged, out.String())
	}

	want := []string{"claude:comprehensive", "codex:external", "claude:external-eval"}
	if got := script.sequence(t); !slices.Equal(got, want) {
		t.Errorf("phase sequence = %v, want %v; output:\n%s", got, want, out.String())
	}

	report, err := os.ReadFile(filepath.Join(repo, ".rrev", "findings.md")) //nolint:gosec // the path is a test temp file
	if err != nil {
		t.Fatalf("no findings report was written: %v", err)
	}
	for _, want := range []string{"add-user-auth", "auth.go:1", "quality", "the handler creates no session", "auth.go:2"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}

	if got := gitOutput(t, repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved from %s to %s during a read-only run", head, got)
	}
	if dirty := gitOutput(t, repo, "status", "--porcelain", "--untracked-files=no"); dirty != "" {
		t.Errorf("a read-only run modified tracked files:\n%s", dirty)
	}
	for _, line := range strings.Split(gitOutput(t, repo, "status", "--porcelain", "--untracked-files=all"), "\n") {
		if line != "" && !strings.Contains(line, ".rrev/") {
			t.Errorf("a read-only run left %q behind, outside its own directory", line)
		}
	}
}

// TestEndToEndConformanceGap covers the point of a spec-driven review: a
// reviewer reads the requirement checklist out of its prompt, reports the
// requirement the branch does not satisfy, and the report names that
// requirement rather than the one that is implemented.
func TestEndToEndConformanceGap(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	addRequirement(t, repo, "add-user-auth")
	script := scriptExecutors(t, map[string]string{
		"claude": `case "$phase:$n" in
  comprehensive:1)
    req=$(requirement 2)
    echo "FINDING: critical | auth.go:1 | conformance | $req | nothing on the branch ends a session"
    ;;
esac`,
		"codex": `echo "<<<RREV:EXTERNAL_DONE>>>"`,
	})
	t.Chdir(repo)

	var out strings.Builder
	if code := run(context.Background(), []string{"--report-only"}, &out, io.Discard); code != status.CodeUnconverged {
		t.Fatalf("code = %d, want %d; output:\n%s", code, status.CodeUnconverged, out.String())
	}
	if got, want := script.sequence(t), []string{"claude:comprehensive", "codex:external"}; !slices.Equal(got, want) {
		t.Fatalf("phase sequence = %v, want %v; output:\n%s", got, want, out.String())
	}

	body, err := os.ReadFile(filepath.Join(repo, ".rrev", "findings.md")) //nolint:gosec // the path is a test temp file
	if err != nil {
		t.Fatalf("no findings report was written: %v", err)
	}
	row := reportRow(t, string(body), "critical")
	for _, want := range []string{"auth: Sign out", "conformance", "auth.go:1"} {
		if !strings.Contains(row, want) {
			t.Errorf("the conformance finding is not attributed to %q:\n%s", want, row)
		}
	}
	if strings.Contains(row, "Sign in") {
		t.Errorf("the gap is reported against the implemented requirement:\n%s", row)
	}
	assertLogged(t, repo, "auth: Sign out")
}

// reportRow returns the report's table row for the given severity.
func reportRow(t *testing.T, report, severity string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "| "+severity+" |") {
			return line
		}
	}
	t.Fatalf("the report has no %s finding:\n%s", severity, report)
	return ""
}

// addRequirement appends a second, unimplemented requirement to the change's
// delta spec, so the checklist a reviewer receives has one requirement the
// branch satisfies and one it does not.
func addRequirement(t *testing.T, repo, change string) {
	t.Helper()
	path := filepath.Join("openspec", "changes", change, "specs", "auth", "spec.md")
	body, err := os.ReadFile(filepath.Join(repo, path)) //nolint:gosec // the path is a test temp file
	if err != nil {
		t.Fatalf("read delta spec: %v", err)
	}
	writeFile(t, repo, path, string(body)+
		"\n### Requirement: Sign out\nThe system SHALL end a session.\n\n"+
		"#### Scenario: Session ended\n- **WHEN** the user signs out\n- **THEN** the session is destroyed\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "specify signing out")
}

// progressLog returns the whole run journal, which is what a later reviewer and
// a human both read.
func progressLog(t *testing.T, repo string) string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(repo, ".rrev", "progress", "*.md"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no progress log was written: %v", err)
	}
	body, err := os.ReadFile(entries[0]) //nolint:gosec // the path is a test temp file
	if err != nil {
		t.Fatalf("read progress log: %v", err)
	}
	return string(body)
}

// standingEntries counts the ledger's rows, which is what tells a folded
// recurrence from a second entry opened for the same argument.
func standingEntries(log string) int {
	_, ledger, found := strings.Cut(log, "## Standing rejections")
	if !found {
		return 0
	}
	n := 0
	for line := range strings.SplitSeq(ledger, "\n") {
		if strings.HasPrefix(line, "- **R") {
			n++
		}
	}
	return n
}

// A declared re-raise has to land on the entry it names. Opening a second entry
// for the same argument is the behaviour that produced 97 rejections in one
// run, two thirds of them re-litigation of a dozen questions.
func TestEndToEndDeclaredReRaiseUpdatesOneLedgerEntry(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	scriptExecutors(t, map[string]string{
		"claude": `case "$phase:$n" in
  comprehensive:1)
    echo "REJECTED: auth.go:9 | quality | the token is echoed | the value is not key material"
    commit "fix something real"
    ;;
  comprehensive:2)
    echo "REJECTED[R1]: auth.go:11 | implementation | the token is echoed | still not key material"
    commit "fix something else"
    ;;
  comprehensive:3) echo "<<<RREV:REVIEW_DONE>>>" ;;
  external-eval:1) echo "<<<RREV:EXTERNAL_DONE>>>" ;;
  final:1) echo "<<<RREV:REVIEW_DONE>>>" ;;
  *) echo "<<<RREV:TASK_FAILED>>>" ;;
esac`,
		"codex": `echo "<<<RREV:EXTERNAL_DONE>>>"`,
	})
	t.Chdir(repo)

	var out strings.Builder
	if code := run(context.Background(), nil, &out, io.Discard); code != status.CodeOK {
		t.Fatalf("code = %d; output:\n%s", code, out.String())
	}
	log := progressLog(t, repo)
	if n := standingEntries(log); n != 1 {
		t.Errorf("ledger holds %d standing rejections, want the two raises folded into one:\n%s", n, log)
	}
	if want := "raised comprehensive 1, 2"; !strings.Contains(log, want) {
		t.Errorf("ledger missing %q:\n%s", want, log)
	}
	// The ledger states one rationale however often the finding is raised, and
	// it is the one that settled the question rather than whatever the latest
	// re-rejection restated. `rejected because:` is the prompt's wording.
	if n := strings.Count(log, "rejected: the value is not key material"); n != 1 {
		t.Errorf("the settling rationale appears %d times, want one ledger statement:\n%s", n, log)
	}
	_, ledger, _ := strings.Cut(log, "## Standing rejections")
	if strings.Contains(ledger, "still not key material") {
		t.Errorf("the re-rejection restated the ledger's rationale:\n%s", log)
	}
}

// Re-litigation crosses phases: in the run that motivated this, the final
// phase re-raised findings the comprehensive phase had rejected ten times.
func TestEndToEndReRaiseAcrossPhasesResolvesToOneEntry(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	scriptExecutors(t, map[string]string{
		"claude": `case "$phase:$n" in
  comprehensive:1)
    echo "REJECTED: auth.go:9 | quality | the token is echoed | the value is not key material"
    commit "fix the handler"
    ;;
  comprehensive:2) echo "<<<RREV:REVIEW_DONE>>>" ;;
  external-eval:1) echo "<<<RREV:EXTERNAL_DONE>>>" ;;
  final:1)
    echo "REJECTED[R1]: auth.go:9 | quality | the token is echoed | still not key material"
    echo "<<<RREV:REVIEW_DONE>>>"
    ;;
  *) echo "<<<RREV:TASK_FAILED>>>" ;;
esac`,
		"codex": `case "$phase:$n" in
  external:1) echo "FINDING: minor | auth.go:2 | external | - | the error path is unhandled" ;;
  *) echo "<<<RREV:EXTERNAL_DONE>>>" ;;
esac`,
	})
	t.Chdir(repo)

	var out strings.Builder
	if code := run(context.Background(), nil, &out, io.Discard); code != status.CodeOK {
		t.Fatalf("code = %d; output:\n%s", code, out.String())
	}

	log := progressLog(t, repo)
	if n := standingEntries(log); n != 1 {
		t.Errorf("ledger holds %d standing rejections, want the cross-phase raises folded into one:\n%s", n, log)
	}
	if want := "raised comprehensive 1; final 1"; !strings.Contains(log, want) {
		t.Errorf("ledger does not record both phases (%q):\n%s", want, log)
	}
}

// A converged external phase that writes nothing is indistinguishable from one
// whose tool died quietly, and the two call for opposite responses.
func TestEndToEndSilentExternalPhaseIsRecorded(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	scriptExecutors(t, map[string]string{
		"claude": `case "$phase:$n" in
  comprehensive:1) echo "<<<RREV:REVIEW_DONE>>>" ;;
  final:1) echo "<<<RREV:REVIEW_DONE>>>" ;;
  *) echo "<<<RREV:TASK_FAILED>>>" ;;
esac`,
		"codex": `echo "<<<RREV:EXTERNAL_DONE>>>"`,
	})
	t.Chdir(repo)

	var out strings.Builder
	if code := run(context.Background(), nil, &out, io.Discard); code != status.CodeOK {
		t.Fatalf("code = %d; output:\n%s", code, out.String())
	}

	if want := "external tool `codex`: no findings reported"; !strings.Contains(progressLog(t, repo), want) {
		t.Errorf("progress log missing %q:\n%s", want, progressLog(t, repo))
	}
}

// Seven reviewers running at once rendered as seven identical lines, and a
// tool call rendered as its bare name. Both are what a user actually watches
// during the longest phase of a run.
func TestEndToEndConsoleAttributesAgentsAndBoundsToolArguments(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	scriptExecutors(t, map[string]string{
		"claude": `case "$phase:$n" in
  comprehensive:1)
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Task","id":"a1","input":{"subagent_type":"conformance"}}]}}'
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Task","id":"a2","input":{"subagent_type":"quality"}}]}}'
    printf '%s\n' '{"type":"assistant","parent_tool_use_id":"a1","message":{"content":[{"type":"text","text":"scenario 3 is not addressed"}]}}'
    printf '%s\n' '{"type":"assistant","parent_tool_use_id":"a2","message":{"content":[{"type":"text","text":"no defects found"}]}}'
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","id":"t1","input":{"command":"go test ./...\nA SECOND LINE THAT MUST NOT BE DISPLAYED"}}]}}'
    printf '%s\n' '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"PASS\nLOTS OF OUTPUT THAT MUST NOT BE DISPLAYED"}]}}'
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"<<<RREV:REVIEW_DONE>>>"}]}}'
    ;;
  final:1) printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"<<<RREV:REVIEW_DONE>>>"}]}}' ;;
  *) printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"<<<RREV:TASK_FAILED>>>"}]}}' ;;
esac`,
		"codex": `echo "<<<RREV:EXTERNAL_DONE>>>"`,
	})
	t.Chdir(repo)

	var out strings.Builder
	if code := run(context.Background(), nil, &out, io.Discard); code != status.CodeOK {
		t.Fatalf("code = %d; output:\n%s", code, out.String())
	}

	got := out.String()
	for _, want := range []string{
		"· agent: conformance",
		"· agent: quality",
		"[conformance] scenario 3 is not addressed",
		"[quality] no defects found",
		"· tool: Bash go test ./... … → ok",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("console missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"A SECOND LINE THAT MUST NOT BE DISPLAYED", "LOTS OF OUTPUT THAT MUST NOT BE DISPLAYED"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("console echoed %q, which floods the display:\n%s", unwanted, got)
		}
	}
}
