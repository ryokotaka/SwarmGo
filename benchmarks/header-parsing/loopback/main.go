//go:build linux

// Command loopback measures the worker's HTTP/1.1 path against a local target:
// requests per second, process CPU time per request and read syscalls per
// request. Run the target from benchmarks/throughput/target.go on other CPUs,
// for example:
//
//	taskset -c 1-3 go run ./benchmarks/throughput/target.go
//	GOMAXPROCS=1 taskset -c 0 go run ./benchmarks/header-parsing/loopback -n 800000 -c 128
//
// A 200,000-request warmup on the same connections precedes the measurement.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	"github.com/ryokotaka/SwarmGo/internal/worker"
)

func cpu() time.Duration {
	var u syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &u)
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}

// syscr is the process's count of read-family syscalls.
func syscr() int64 {
	b, _ := os.ReadFile("/proc/self/io")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "syscr:") {
			var v int64
			fmt.Sscan(strings.TrimSpace(l[6:]), &v)
			return v
		}
	}
	return 0
}

func main() {
	n := flag.Int("n", 2000000, "requests")
	c := flag.Int("c", 256, "concurrency")
	target := flag.String("url", "http://127.0.0.1:8080/work", "target URL")
	prof := flag.String("cpuprofile", "", "write a CPU profile of the measured run")
	flag.Parse()
	body := []byte(`{"data":"` + strings.Repeat("x", 1013) + `"}`)
	opts := worker.RequestOptions{Method: "POST", Body: body, Headers: map[string]string{"Content-Type": "application/json"}}
	r := worker.NewMyRunnerWithConcurrency(*c)
	if _, err := r.MyRunWithOptions(context.Background(), *target, 200000, *c, opts, nil); err != nil {
		panic(err)
	}
	if *prof != "" {
		f, err := os.Create(*prof)
		if err != nil {
			panic(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	io0 := syscr()
	c0 := cpu()
	s, err := r.MyRunWithOptions(context.Background(), *target, *n, *c, opts, nil)
	if err != nil {
		panic(err)
	}
	used := cpu() - c0
	fmt.Printf("syscr_per_req=%.2f ", float64(syscr()-io0)/float64(*n))
	fmt.Printf("rps=%.0f ok=%d fail=%d cpu_ns_per_req=%.0f p50=%v p99=%v\n", float64(s.MyTotal)/s.Elapsed.Seconds(), s.MySuccess, s.MyFailed, float64(used.Nanoseconds())/float64(s.MyTotal), s.LatencyP50, s.LatencyP99)
}
