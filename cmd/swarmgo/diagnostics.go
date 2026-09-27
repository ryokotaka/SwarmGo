package main

import (
	"fmt"
	"os"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"sync"
	"time"
)

// startDiagnostics records a CPU profile and a short execution trace when the
// benchmark harness asks for them through the environment. Neither is part of
// the documented interface; both exist to find where a run spends its time.
//
//	SWARMGO_CPUPROFILE=path  CPU profile for the whole process
//	SWARMGO_TRACE=path       execution trace, SWARMGO_TRACE_SECONDS long (default 2),
//	                         starting SWARMGO_TRACE_AFTER seconds in (default 15)
//
// The returned function stops both and must run before the process exits.
func startDiagnostics() func() {
	var stops []func()
	if path := os.Getenv("SWARMGO_CPUPROFILE"); path != "" {
		if f, err := os.Create(path); err != nil {
			fmt.Fprintln(os.Stderr, "cpu profile:", err)
		} else if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(os.Stderr, "cpu profile:", err)
			f.Close()
		} else {
			stops = append(stops, func() { pprof.StopCPUProfile(); f.Close() })
		}
	}
	if path := os.Getenv("SWARMGO_TRACE"); path != "" {
		after := diagnosticSeconds("SWARMGO_TRACE_AFTER", 15)
		length := diagnosticSeconds("SWARMGO_TRACE_SECONDS", 2)
		var once sync.Once
		var f *os.File
		stop := func() {
			once.Do(func() {
				if f != nil {
					trace.Stop()
					f.Close()
				}
			})
		}
		var mu sync.Mutex
		time.AfterFunc(after, func() {
			mu.Lock()
			defer mu.Unlock()
			var err error
			if f, err = os.Create(path); err != nil {
				fmt.Fprintln(os.Stderr, "trace:", err)
				f = nil
				return
			}
			if err := trace.Start(f); err != nil {
				fmt.Fprintln(os.Stderr, "trace:", err)
				f.Close()
				f = nil
				return
			}
			time.AfterFunc(length, func() { mu.Lock(); defer mu.Unlock(); stop() })
		})
		stops = append(stops, func() { mu.Lock(); defer mu.Unlock(); stop() })
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}

func diagnosticSeconds(name string, fallback float64) time.Duration {
	if value, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && value >= 0 {
		fallback = value
	}
	return time.Duration(fallback * float64(time.Second))
}
