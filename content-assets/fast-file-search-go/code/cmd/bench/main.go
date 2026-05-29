// Bench runs every registered variant against the test file and prints a table.
//
// Usage:
//
//	bench -file=/path/to/2g.bin [-runs=5] [-only=v4-mmap,v6-mmap-parallel]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/segflow/blog-search-in-file/internal/search"
	"github.com/segflow/blog-search-in-file/internal/testfile"

	// Register all variants.
	_ "github.com/segflow/blog-search-in-file/variants/v1_naive"
	_ "github.com/segflow/blog-search-in-file/variants/v2_bufio"
	_ "github.com/segflow/blog-search-in-file/variants/v3_chunked"
	_ "github.com/segflow/blog-search-in-file/variants/v4_mmap"
	_ "github.com/segflow/blog-search-in-file/variants/v5_madvise"
	_ "github.com/segflow/blog-search-in-file/variants/v6_parallel"
	_ "github.com/segflow/blog-search-in-file/variants/v7_pread_parallel"
	_ "github.com/segflow/blog-search-in-file/variants/v8_simd"
	_ "github.com/segflow/blog-search-in-file/variants/v9_combo"
	_ "github.com/segflow/blog-search-in-file/variants/v10_populate"
	_ "github.com/segflow/blog-search-in-file/variants/v11_pread_simd"
	_ "github.com/segflow/blog-search-in-file/variants/v12_archsimd"
	_ "github.com/segflow/blog-search-in-file/variants/v13_pread_archsimd"
)

type Row struct {
	Variant string    `json:"variant"`
	Runs    []float64 `json:"runs_seconds"`
	Mean    float64   `json:"mean_seconds"`
	MBs     float64   `json:"mb_per_second"`
}

func main() {
	file := flag.String("file", "/tmp/haystack2g.bin", "test file path")
	runs := flag.Int("runs", 5, "number of timed runs per variant")
	only := flag.String("only", "", "comma-separated subset of variant names to run")
	jsonOut := flag.String("json", "", "if set, write results JSON to this path")
	warm := flag.Bool("warm", true, "pre-warm the page cache before each variant")
	cold := flag.Bool("cold", false, "evict the file from page cache before each run via posix_fadvise(DONTNEED)")
	preCmd := flag.String("pre-each", "", "shell command to run before each timed iteration (in addition to -cold). Useful on WSL2 to also empty the Windows standby list, e.g. -pre-each '/mnt/c/Windows/System32/cmd.exe /c C:\\\\Windows\\\\Temp\\\\RAMMap.exe -Et'")
	profDir := flag.String("profile-dir", "", "if set, write a CPU profile per variant to <dir>/<variant>.prof")
	profRuns := flag.Int("profile-runs", 10, "when -profile-dir is set, run the search this many times inside the profile window so we capture enough samples")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "ensuring %s (%d bytes)...\n", *file, testfile.Size)
	if err := testfile.Ensure(*file); err != nil {
		die("ensure: %v", err)
	}

	off, value, err := testfile.Plant(*file)
	if err != nil {
		die("plant: %v", err)
	}
	fmt.Fprintf(os.Stderr, "needle 0x%x at offset %d\n", uint64(value), off)

	if *warm {
		fmt.Fprintf(os.Stderr, "warming page cache...\n")
		if err := testfile.Warm(*file); err != nil {
			die("warm: %v", err)
		}
	}

	allVariants := search.All()
	names := make([]string, 0, len(allVariants))
	for n := range allVariants {
		names = append(names, n)
	}
	sort.Strings(names)

	if *only != "" {
		wanted := strings.Split(*only, ",")
		names = slices.DeleteFunc(names, func(n string) bool {
			return !slices.Contains(wanted, n)
		})
	}

	rows := make([]Row, 0, len(names))
	for _, name := range names {
		s := allVariants[name]
		fmt.Fprintf(os.Stderr, "\n== %s ==\n", name)
		row := Row{Variant: name, Runs: make([]float64, 0, *runs)}
		for i := 0; i < *runs; i++ {
			if *cold {
				if err := dropCache(*file); err != nil {
					die("drop cache: %v", err)
				}
			}
			if *preCmd != "" {
				cmd := exec.Command("sh", "-c", *preCmd)
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					die("pre-each: %v", err)
				}
				// Give the system a beat to release pages.
				time.Sleep(200 * time.Millisecond)
			}
			start := time.Now()
			got, err := s.Search(*file)
			dur := time.Since(start)
			if err != nil {
				die("%s: %v", name, err)
			}
			if got != off {
				die("%s: wrong offset got=%d want=%d", name, got, off)
			}
			secs := dur.Seconds()
			row.Runs = append(row.Runs, secs)
			fmt.Fprintf(os.Stderr, "  run %d: %.3fs  (%.1f MB/s)\n", i+1, secs, float64(testfile.Size)/secs/1e6)
		}
		row.Mean = trimmedMean(row.Runs)
		row.MBs = float64(testfile.Size) / row.Mean / 1e6
		rows = append(rows, row)

		if *profDir != "" {
			if err := os.MkdirAll(*profDir, 0o755); err != nil {
				die("mkdir %s: %v", *profDir, err)
			}
			profPath := filepath.Join(*profDir, name+".prof")
			pf, err := os.Create(profPath)
			if err != nil {
				die("create %s: %v", profPath, err)
			}
			if err := pprof.StartCPUProfile(pf); err != nil {
				die("start profile: %v", err)
			}
			for j := 0; j < *profRuns; j++ {
				if _, err := s.Search(*file); err != nil {
					die("%s (profile): %v", name, err)
				}
			}
			pprof.StopCPUProfile()
			pf.Close()
			fmt.Fprintf(os.Stderr, "  wrote %s (%d runs)\n", profPath, *profRuns)
		}
	}

	// Pretty print.
	fmt.Println()
	fmt.Printf("%-28s  %10s  %10s\n", "variant", "mean(s)", "MB/s")
	fmt.Printf("%-28s  %10s  %10s\n", strings.Repeat("-", 28), strings.Repeat("-", 10), strings.Repeat("-", 10))
	for _, r := range rows {
		fmt.Printf("%-28s  %10.3f  %10.0f\n", r.Variant, r.Mean, r.MBs)
	}

	if *jsonOut != "" {
		b, _ := json.MarshalIndent(rows, "", "  ")
		_ = os.WriteFile(*jsonOut, b, 0o644)
	}
}

func trimmedMean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := slices.Clone(xs)
	sort.Float64s(cp)
	if len(cp) >= 5 {
		cp = cp[1 : len(cp)-1] // drop slowest and fastest
	}
	var sum float64
	for _, x := range cp {
		sum += x
	}
	return sum / float64(len(cp))
}

func dropCache(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// Flush dirty pages first, then ask the kernel to drop them.
	_ = unix.Fdatasync(int(f.Fd()))
	return unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
