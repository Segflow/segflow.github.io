// Package v13preadarchsimd is v7 (parallel pread) with v12's pure-Go AVX-512
// scan kernel as the inner loop. No .s files, just generic Go + the
// experimental simd/archsimd package.
//
// Requires GOEXPERIMENT=simd.
package v13preadarchsimd

import (
	"math/bits"
	"os"
	"github.com/segflow/blog-search-in-file/internal/search"
	"simd/archsimd"
	"sync"
	"sync/atomic"
	"unsafe"

)

type S struct{}

func (S) Name() string { return "v13-pread-archsimd" }

const chunkSize = 1 << 20 // 1 MiB

func (S) Search(path string) (int64, error) {
	if !archsimd.X86.AVX512() {
		return -1, errString("v13: CPU lacks AVX-512")
	}
	st, err := os.Stat(path)
	if err != nil {
		return -1, err
	}
	size := st.Size()

	nWorkers := search.Workers()
	per := (size + int64(nWorkers) - 1) / int64(nWorkers)
	if per%chunkSize != 0 {
		per += chunkSize - per%chunkSize
	}

	var found atomic.Int64
	found.Store(-1)
	var wg sync.WaitGroup
	errs := make(chan error, nWorkers)
	for w := range nWorkers {
		start := int64(w) * per
		end := min(start+per, size)
		if start >= end {
			continue
		}
		wg.Add(1)
		go func(start, end int64) {
			defer wg.Done()
			f, err := os.Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer f.Close()
			buf := make([]byte, chunkSize)
			off := start
			for off < end {
				want := chunkSize
				if int64(want) > end-off {
					want = int(end - off)
				}
				n, err := f.ReadAt(buf[:want], off)
				if err != nil && n == 0 {
					errs <- err
					return
				}
				if idx := scanZero(buf[:n]); idx >= 0 {
					hit := off + int64(idx)
					hitOff := hit &^ 7
					for {
						cur := found.Load()
						if cur != -1 && cur <= hitOff {
							return
						}
						if found.CompareAndSwap(cur, hitOff) {
							return
						}
					}
				}
				off += int64(n)
				if cur := found.Load(); cur != -1 && cur < off {
					return
				}
			}
		}(start, end)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return -1, e
		}
	}
	return found.Load(), nil
}

// scanZero is identical to v12's; replicated here so each variant is
// self-contained and easy to read.
func scanZero(buf []byte) int {
	const step = 128
	zero := archsimd.BroadcastUint8x64(0)
	n := len(buf) &^ (step - 1)
	for i := 0; i < n; i += step {
		v0 := archsimd.LoadUint8x64((*[64]uint8)(unsafe.Pointer(&buf[i])))
		v1 := archsimd.LoadUint8x64((*[64]uint8)(unsafe.Pointer(&buf[i+64])))
		m0 := v0.Equal(zero).ToBits()
		m1 := v1.Equal(zero).ToBits()
		if m0 != ^uint64(0) || m1 != ^uint64(0) {
			if m0 != ^uint64(0) {
				return i + bits.TrailingZeros64(^m0)
			}
			return i + 64 + bits.TrailingZeros64(^m1)
		}
	}
	for i := n; i < len(buf); i++ {
		if buf[i] != 0 {
			return i
		}
	}
	return -1
}

type errString string

func (e errString) Error() string { return string(e) }

func init() { search.Register(S{}) }
