//go:build !unix && !windows

package harness

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
