package progress

import (
	"errors"
	"os"
	"time"
)

// errLockBusy reports that another writer held the lock for the whole wait.
var errLockBusy = errors.New("progress log is locked by another run")

// DefaultLockWait bounds how long an append waits for a competing run. The
// pipeline must not stall on logging, so the wait is short and expiring it
// degrades to an unserialized append rather than to an error.
const DefaultLockWait = 2 * time.Second

// lockPollInterval is how often a waiting writer retries the exclusive create.
const lockPollInterval = 5 * time.Millisecond

// staleLockAge is how long a lock file may sit untouched before a waiter takes
// it over. A run killed outright leaves the file behind, and without reclaim
// every append of every later run would pay the full wait forever.
const staleLockAge = 2 * time.Minute

// fileLock is an advisory lock built from an exclusively created file, which
// works the same across platforms and across processes that never share a
// file descriptor.
type fileLock struct {
	path string
}

// acquire blocks until the lock is held or wait elapses, returning errLockBusy
// on expiry.
func (l fileLock) acquire(wait time.Duration) error {
	deadline := time.Now().Add(wait)
	// One reclaim attempt only: a lock file that cannot be removed would
	// otherwise spin here forever.
	reclaimed := false
	for {
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if err := f.Close(); err != nil {
				// The caller reads any error as "lock not held" and will not
				// release it, so the file must not outlive this attempt.
				l.release()
				return err
			}
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if !reclaimed && l.stale() {
			reclaimed = true
			l.release()
			continue
		}
		if time.Now().After(deadline) {
			return errLockBusy
		}
		time.Sleep(lockPollInterval)
	}
}

// stale reports whether the lock file has sat untouched long enough that its
// owner is presumed dead. A missing file is not stale: the next create wins it.
func (l fileLock) stale() bool {
	info, err := os.Stat(l.path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > staleLockAge
}

func (l fileLock) release() {
	_ = os.Remove(l.path)
}
