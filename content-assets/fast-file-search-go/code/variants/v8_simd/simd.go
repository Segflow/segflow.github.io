// Package v8simd mmaps the file and scans it with an AVX2 inner loop written
// in Go assembly. The kernel does 4 KiB minor page faults same as v4, but the
// hot loop only spends ~4 instructions per 128 bytes of haystack.
//
// AVX2 background: the AMD Zen / Intel Skylake+ chips can issue two 256-bit
// loads per cycle, two 256-bit ALU ops per cycle, and a fast vptest that
// fuses the OR+branch. So processing 128 bytes costs about 4 µops issued in
// ~2 cycles on the front-end. The bottleneck moves entirely to DRAM/L3.
package v8simd

import (
	"os"
	"unsafe"

	"github.com/segflow/blog-search-in-file/internal/search"
	"golang.org/x/sys/cpu"
	"golang.org/x/sys/unix"
)

type S struct{}

func (S) Name() string { return "v8-simd-avx2" }

// firstNonZeroAVX2 is implemented in simd_amd64.s.
//
//go:noescape
func firstNonZeroAVX2(buf *byte, n int) int

func (S) Search(path string) (int64, error) {
	if !cpu.X86.HasAVX2 {
		return -1, errNoAVX2
	}
	f, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return -1, err
	}
	size := int(st.Size())

	data, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return -1, err
	}
	defer unix.Munmap(data)
	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)
	_ = unix.Madvise(data, unix.MADV_WILLNEED)

	// Round size down to a multiple of 128 for the vector loop; handle the
	// tail bytes scalarly.
	vecN := size &^ 127
	idx := firstNonZeroAVX2(&data[0], vecN)
	if idx >= 0 {
		// Round down to 8-byte boundary.
		return int64(idx) &^ 7, nil
	}
	// Scalar tail.
	for i := vecN; i < size; i++ {
		if data[i] != 0 {
			return int64(i) &^ 7, nil
		}
	}
	return -1, nil
}

// sentinel.
type errString string

func (e errString) Error() string { return string(e) }

const errNoAVX2 = errString("v8: CPU lacks AVX2")

// keep unused-import safe
var _ = unsafe.Sizeof(uint64(0))

func init() { search.Register(S{}) }
