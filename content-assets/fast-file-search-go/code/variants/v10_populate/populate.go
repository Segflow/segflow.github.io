// Package v10populate mmaps the file with MAP_POPULATE so the kernel
// pre-faults every page during the mmap call itself. Combined with parallel
// shards and the AVX2 inner loop, the userland scan should hit pure DRAM
// bandwidth -- no page faults stalls inside the loop, no copy_to_user from
// pread.
//
// MAP_POPULATE: a Linux-specific mmap flag that walks the page table during
// mmap() and brings every page in. For an already-cached file this just
// touches each page once; for a cold file it triggers async readahead. After
// mmap returns, the scan loop never takes a page fault.
package v10populate

import (
	"os"
	"github.com/segflow/blog-search-in-file/internal/search"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/cpu"
	"golang.org/x/sys/unix"
)

type S struct{}

func (S) Name() string { return "v10-populate-parallel-simd" }

//go:noescape
func firstNonZeroAVX2(buf *byte, n int) int

func (S) Search(path string) (int64, error) {
	if !cpu.X86.HasAVX2 {
		return -1, errString("v10: CPU lacks AVX2")
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

	data, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ,
		unix.MAP_SHARED|unix.MAP_POPULATE)
	if err != nil {
		return -1, err
	}
	defer unix.Munmap(data)
	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)

	nWorkers := search.Workers()
	const align = 128
	per := (size + nWorkers - 1) / nWorkers
	per = (per + align - 1) &^ (align - 1)

	var found atomic.Int64
	found.Store(-1)
	var wg sync.WaitGroup
	for w := range nWorkers {
		start := w * per
		end := min(start+per, size)
		if start >= end {
			continue
		}
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			n := end - start
			vecN := n &^ 127
			idx := firstNonZeroAVX2((*byte)(unsafe.Pointer(&data[start])), vecN)
			hit := -1
			if idx >= 0 {
				hit = start + idx
			} else {
				for i := start + vecN; i < end; i++ {
					if data[i] != 0 {
						hit = i
						break
					}
				}
			}
			if hit < 0 {
				return
			}
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
		}(start, end)
	}
	wg.Wait()
	return found.Load(), nil
}

type errString string

func (e errString) Error() string { return string(e) }

func init() { search.Register(S{}) }
