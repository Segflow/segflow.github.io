// Package v9combo: mmap the file, split it across CPUs, run the AVX2 kernel
// on each shard. This is the all-engines-on configuration: parallel readers,
// SIMD scan, kernel page cache, no userland copies.
package v9combo

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

func (S) Name() string { return "v9-parallel-simd" }

//go:noescape
func firstNonZeroAVX2(buf *byte, n int) int

func (S) Search(path string) (int64, error) {
	if !cpu.X86.HasAVX2 {
		return -1, errString("v9: CPU lacks AVX2")
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

	nWorkers := search.Workers()
	// Shard must be a multiple of 128 so the SIMD loop never runs off the end
	// of its slice into another worker's territory.
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
