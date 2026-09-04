package harness

import (
	"fmt"
	"os"
	"time"
)

// stateLock is an atomic mkdir lock. Unlike advisory-lock APIs it has the same
// first-writer-wins behavior on every platform Go supports. A run's lock is
// normally held for milliseconds. It is deliberately never stolen: a process
// killed inside the critical section can leave a lock that makes later state
// operations time out until the lock directory is removed.
type stateLock struct{ path string }

func lockFile(path string) (*stateLock, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return &stateLock{path: path}, nil
		}
		if !os.IsExist(err) || !time.Now().Before(deadline) {
			return nil, fmt.Errorf("lock run state: %w", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func unlockFile(lock *stateLock) { _ = os.Remove(lock.path) }
