package progress

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// idPrefix starts every ledger identifier. Identifiers are short because a
// reviewer has to read one out of a prompt and write it back into a report
// line; anything longer invites transcription errors.
const idPrefix = "R"

// raise is one occasion on which a finding was reported. Phase is carried
// alongside the iteration because re-litigation crosses phases: a later phase's
// reviewers re-raise what an earlier phase rejected.
type raise struct {
	Phase     string
	Iteration int
}

// ledgerEntry is one distinct finding tracked across a whole run. It is built
// from the findings as they are recorded and rendered into the log's ledger
// section; the recorded findings remain the source of truth.
type ledgerEntry struct {
	ID string
	// Locations holds every location the finding was reported at, not only the
	// first. A wrongly declared re-raise merges two distinct findings, and an
	// entry spanning unrelated files is how that becomes visible.
	Locations []string
	Claim     string
	Rationale string
	Raises    []raise
	// Confirmed marks an entry that was later confirmed and fixed, so a reader
	// is not told a resolved issue is still standing.
	Confirmed bool
}

// standing reports whether the entry still represents an open rejection.
func (e *ledgerEntry) standing() bool { return !e.Confirmed && e.Rationale != "" }

// addRaise records another occasion, ignoring a repeat of one already held so
// that re-rendering never inflates the count.
func (e *ledgerEntry) addRaise(r raise) {
	if slices.Contains(e.Raises, r) {
		return
	}
	e.Raises = append(e.Raises, r)
}

func (e *ledgerEntry) addLocation(loc string) {
	if loc == "" || slices.Contains(e.Locations, loc) {
		return
	}
	e.Locations = append(e.Locations, loc)
}

// nextID hands out the next identifier. Sequential rather than derived from the
// finding's content: an identifier must stay stable while the finding's file,
// line, and wording all drift between iterations.
func (l *Log) nextID() string {
	l.seq++
	return idPrefix + strconv.Itoa(l.seq)
}

// resolve settles which ledger entry a finding belongs to and returns the entry
// along with a note when the executor named an identifier the log does not
// hold. rrev never infers a match: an undeclared finding is a new finding, so a
// missed recurrence costs accuracy rather than merging two real issues.
func (l *Log) resolve(f Finding) (*ledgerEntry, string) {
	if f.ReRaises == "" {
		return l.newEntry(), ""
	}
	if e, ok := l.ledger[f.ReRaises]; ok {
		return e, ""
	}
	e := l.newEntry()
	return e, fmt.Sprintf("re-raise of unknown entry %s recorded as new finding %s", f.ReRaises, e.ID)
}

func (l *Log) newEntry() *ledgerEntry {
	e := &ledgerEntry{ID: l.nextID()}
	l.ledger[e.ID] = e
	l.order = append(l.order, e.ID)
	return e
}

// standingEntries returns the open rejections, most-raised first, which is the
// order a reader and a truncated prompt both want: the thing raised eight times
// matters more than the thing raised once.
func (l *Log) standingEntries() []*ledgerEntry {
	var out []*ledgerEntry
	for _, id := range l.order {
		if e := l.ledger[id]; e.standing() {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Raises) > len(out[j].Raises) })
	return out
}

// ledgerHeading opens the ledger section. Writing it as a heading keeps the
// section findable both by a reader skimming and by the code that replaces it.
const ledgerHeading = "## Standing rejections"

// renderLedger produces the whole ledger section, or the empty string when
// nothing is standing so an untroubled run carries no empty scaffolding.
func (l *Log) renderLedger() string {
	entries := l.standingEntries()
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nAlready raised and rejected. A reviewer reporting one of these must name its\n"+
		"id, and either accept the stated reason or say why it is wrong.\n\n", ledgerHeading)
	for _, e := range entries {
		fmt.Fprintf(&b, "- **%s** `%s` — raised %s\n", e.ID, strings.Join(e.Locations, ", "), describeRaises(e.Raises))
		// A rejection reported without a separate claim carries the reason as
		// its only text; printing it twice tells the reader nothing.
		if e.Claim != "" && oneLine(e.Claim) != oneLine(e.Rationale) {
			fmt.Fprintf(&b, "  claim: %s\n", oneLine(e.Claim))
		}
		fmt.Fprintf(&b, "  rejected: %s\n", oneLine(e.Rationale))
	}
	return b.String()
}

// describeRaises names every phase and iteration that raised the entry, which
// is the whole point of the ledger: "raised in comprehensive 1, 4, 7" is the
// sentence the prose in an unstructured log was trying to write by hand.
func describeRaises(raises []raise) string {
	if len(raises) == 0 {
		return "not yet"
	}
	byPhase := map[string][]int{}
	var phases []string
	for _, r := range raises {
		if _, seen := byPhase[r.Phase]; !seen {
			phases = append(phases, r.Phase)
		}
		byPhase[r.Phase] = append(byPhase[r.Phase], r.Iteration)
	}
	parts := make([]string, 0, len(phases))
	for _, p := range phases {
		nums := make([]string, 0, len(byPhase[p]))
		for _, n := range byPhase[p] {
			nums = append(nums, strconv.Itoa(n))
		}
		parts = append(parts, strings.TrimSpace(p+" "+strings.Join(nums, ", ")))
	}
	return strings.Join(parts, "; ")
}

// oneLine flattens text that may span lines, so a ledger entry stays one
// scannable row however the executor wrote its rationale.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
