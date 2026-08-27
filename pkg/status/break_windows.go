//go:build windows

package status

import "os"

// BreakSignal reports that there is no break signal: Windows delivers no
// second interrupt rrev could tell apart from the abort, so the loop terminates
// only on its own conditions and the hint is left out of the banner.
func BreakSignal() (os.Signal, string, bool) { return nil, "", false }
