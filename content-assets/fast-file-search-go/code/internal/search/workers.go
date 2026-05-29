package search

import (
	"os"
	"runtime"
	"strconv"
)

// Workers returns the number of worker goroutines parallel variants should
// spawn. It checks the WORKERS environment variable first (handy for
// scaling sweeps from the bench tool), falling back to runtime.NumCPU().
func Workers() int {
	if v := os.Getenv("WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return runtime.NumCPU()
}
