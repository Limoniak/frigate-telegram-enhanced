// Package atomicfile writes the files of the data volume (state, activity, settings,
// connection) so that a crash or a full disk never leaves a truncated one behind.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write replaces path with b, readable by the owner only: b goes to a temporary file
// of the same directory, renamed over path once complete. On failure path is left
// as it was and the temporary file is removed.
func Write(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*") // created 0600
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no effect after a successful rename
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
