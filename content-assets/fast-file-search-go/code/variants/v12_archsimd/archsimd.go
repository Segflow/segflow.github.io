// Package v12archsimd uses Go's experimental [simd/archsimd] package to
// express the AVX-512 scan in pure Go (no .s file). It mmaps the file, then
// the inner loop loads two 64-byte vectors, ORs them, and compares against
// the zero vector. The compare yields a 128-bit (well, 2x64-bit) mask; if
// the mask is all-ones, every byte was zero and we move on. Otherwise we
// narrow down with a scalar tail.
//
// This is single-threaded so we can compare its CPU cost head to head with
// the hand-written AVX2 assembly in v8.
//
// Requires GOEXPERIMENT=simd.
package v12archsimd

import (
	"math/bits"
	"os"
	"simd/archsimd"
	"unsafe"

	"github.com/segflow/blog-search-in-file/internal/search"
	"golang.org/x/sys/unix"
)

type S struct{}

func (S) Name() string { return "v12-archsimd-avx512" }

func (S) Search(path string) (int64, error) {
	if !archsimd.X86.AVX512() {
		return -1, errString("v12: CPU lacks AVX-512")
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

	idx := scanZero(data)
	if idx < 0 {
		return -1, nil
	}
	return int64(idx) &^ 7, nil
}

// scanZero returns the byte index of the first non-zero byte in buf, or -1.
//
// We process 128 bytes per loop iteration: two AVX-512 loads of 64 bytes
// each, OR them, compare-equal to zero, AND the two masks together; if the
// resulting 128-bit mask has any clear bit, there is a non-zero byte in this
// window. The pointer-math version (instead of LoadUint8x64Slice on a
// reslice) is what the compiler turns into a bounds-check-free hot loop.
func scanZero(buf []byte) int {
	const step = 128
	zero := archsimd.BroadcastUint8x64(0)
	n := len(buf) &^ (step - 1)
	for i := 0; i < n; i += step {
		v0 := archsimd.LoadUint8x64((*[64]uint8)(unsafe.Pointer(&buf[i])))
		v1 := archsimd.LoadUint8x64((*[64]uint8)(unsafe.Pointer(&buf[i+64])))
		// Mask bit = 1 where byte == 0. If any bit is 0, we have a hit.
		m0 := v0.Equal(zero).ToBits()
		m1 := v1.Equal(zero).ToBits()
		if m0 != ^uint64(0) || m1 != ^uint64(0) {
			// Find the exact byte in this 128-byte window.
			if m0 != ^uint64(0) {
				return i + firstZeroBit(m0)
			}
			return i + 64 + firstZeroBit(m1)
		}
	}
	// Scalar tail.
	for i := n; i < len(buf); i++ {
		if buf[i] != 0 {
			return i
		}
	}
	return -1
}

// firstZeroBit returns the position of the first zero bit in mask (i.e. the
// first byte that compared non-zero). math/bits.TrailingZeros64 lowers to
// TZCNT on amd64.
func firstZeroBit(mask uint64) int {
	return bits.TrailingZeros64(^mask)
}

type errString string

func (e errString) Error() string { return string(e) }

func init() { search.Register(S{}) }
