// Package v11preadsimd is v7 (parallel pread) with the AVX2 inner scan from
// v8. Each worker does its own pread into a per-CPU buffer, then runs the
// SIMD kernel over that buffer.
package v11preadsimd

import (
	"os"
	"github.com/segflow/blog-search-in-file/internal/search"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/cpu"
)

type S struct{}

func (S) Name() string { return "v11-pread-simd" }

//go:noescape
func firstNonZeroAVX2(buf *byte, n int) int

const chunkSize = 1 << 20 // 1 MiB

func (S) Search(path string) (int64, error) {
	if !cpu.X86.HasAVX2 {
		return -1, errString("v11: CPU lacks AVX2")
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
				vecN := n &^ 127
				idx := firstNonZeroAVX2((*byte)(unsafe.Pointer(&buf[0])), vecN)
				hit := -1
				if idx >= 0 {
					hit = int(off) + idx
				} else {
					for i := vecN; i < n; i++ {
						if buf[i] != 0 {
							hit = int(off) + i
							break
						}
					}
				}
				if hit >= 0 {
					hitOff := int64(hit) &^ 7
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

type errString string

func (e errString) Error() string { return string(e) }

func init() { search.Register(S{}) }
