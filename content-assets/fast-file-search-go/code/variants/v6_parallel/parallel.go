// Package v6parallel mmaps the file then splits it into N shards (N = NumCPU)
// and races one goroutine per shard. Each goroutine scans its slice as
// []uint64 and reports the first non-zero offset; we return the minimum.
//
// Why this is faster than v5 even when everything is in RAM:
//
//   - Single-core memory bandwidth on Zen 5 tops out around 15-25 GB/s for a
//     pure load loop. Multiple cores together push 50+ GB/s by interleaving
//     requests across DRAM channels and using more in-flight cache misses.
//   - When the cache is cold, multiple cores trigger more parallel page faults
//     which translate to more queue depth to the storage stack.
package v6parallel

import (
	"os"
	"github.com/segflow/blog-search-in-file/internal/search"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

type S struct{}

func (S) Name() string { return "v6-mmap-parallel" }

func (S) Search(path string) (int64, error) {
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
	// Shard on 8-byte boundary so the uint64 reinterpret is always aligned.
	totalWords := int64(size / 8)
	per := (totalWords + int64(nWorkers) - 1) / int64(nWorkers)

	var found atomic.Int64
	found.Store(-1)
	var wg sync.WaitGroup
	for w := range nWorkers {
		startWord := int64(w) * per
		endWord := min(startWord+per, totalWords)
		if startWord >= endWord {
			continue
		}
		wg.Add(1)
		go func(startWord, endWord int64) {
			defer wg.Done()
			base := unsafe.Pointer(&data[startWord*8])
			n := int(endWord - startWord)
			words := unsafe.Slice((*uint64)(base), n)
			for i, x := range words {
				if x != 0 {
					off := (startWord + int64(i)) * 8
					// Use CAS to keep the smallest offset only.
					for {
						cur := found.Load()
						if cur != -1 && cur <= off {
							return
						}
						if found.CompareAndSwap(cur, off) {
							return
						}
					}
				}
			}
		}(startWord, endWord)
	}
	wg.Wait()
	return found.Load(), nil
}

func init() { search.Register(S{}) }
