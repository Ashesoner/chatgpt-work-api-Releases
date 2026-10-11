//go:build !windows

package tunnelnetwork

import "os"

func replaceFileAtomic(source, destination string) error {
	return os.Rename(source, destination)
}
