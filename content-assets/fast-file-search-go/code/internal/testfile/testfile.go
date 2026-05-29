// Package testfile creates and re-seeds the 2 GiB haystack used by every variant.
package testfile

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
)

// Size is the size of the haystack in bytes (4 GiB).
const Size int64 = 4 * 1024 * 1024 * 1024

// NeedleOffset is the fixed offset at which the needle lives. Pinning it to
// the last aligned slot means every variant scans the whole file end to end
// and there is no luck factor across runs or programs.
const NeedleOffset int64 = Size - 8

// Ensure makes sure a file of exactly Size bytes filled with zeros exists at
// path. If the file already has the right size we leave its bytes alone (apart
// from the needle re-plant), so the page cache is preserved across runs.
func Ensure(path string) error {
	st, err := os.Stat(path)
	if err == nil && st.Size() == Size {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(Size); err != nil {
		return err
	}
	// Force the blocks to be physically allocated so warm and cold runs are
	// honest. Without this the file is sparse and every "read" comes back as
	// zero without ever touching storage.
	zero := make([]byte, 1<<20) // 1 MiB
	var off int64
	for off < Size {
		n, err := f.WriteAt(zero, off)
		if err != nil {
			return err
		}
		off += int64(n)
	}
	return f.Sync()
}

// Plant makes sure the file at path has a non-zero int64 at NeedleOffset and
// zeros everywhere else. It is idempotent: if the right value is already
// there, it leaves the file (and its page-cache state) untouched.
//
// Returns (NeedleOffset, value). The needle is a fixed magic constant so it
// stays at the same bytes across program invocations as well, which means we
// never need to re-plant after the first run.
func Plant(path string) (int64, int64, error) {
	const magic int64 = 0x4e_45_45_44_4c_45_21_21 // "NEEDLE!!" big-endian-ish

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var cur [8]byte
	if _, err := f.ReadAt(cur[:], NeedleOffset); err != nil {
		return 0, 0, err
	}
	if int64(binary.LittleEndian.Uint64(cur[:])) == magic {
		return NeedleOffset, magic, nil
	}

	var vb [8]byte
	binary.LittleEndian.PutUint64(vb[:], uint64(magic))
	if _, err := f.WriteAt(vb[:], NeedleOffset); err != nil {
		return 0, 0, err
	}
	return NeedleOffset, magic, nil
}

// keep imports stable.
var _ = rand.Reader

// Warm reads the whole file once so subsequent runs hit the page cache.
func Warm(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	for {
		_, err := f.Read(buf)
		if err != nil {
			break
		}
	}
	return nil
}

// MustEnsure is the panicking helper used by main packages.
func MustEnsure(path string) {
	if err := Ensure(path); err != nil {
		panic(fmt.Errorf("ensure %s: %w", path, err))
	}
}
