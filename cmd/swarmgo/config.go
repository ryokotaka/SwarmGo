package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ryokotaka/SwarmGo/internal/scenario"
	"github.com/ryokotaka/SwarmGo/internal/worker"
)

// stdout receives -print output; tests replace it.
var stdout io.Writer = os.Stdout

// errPrinted ends a command after -print, without sending anything.
var errPrinted = errors.New("printed requests")

type configFlags struct {
	path  string
	print int
}

func addConfigFlags(flags *flag.FlagSet) *configFlags {
	c := &configFlags{}
	flags.StringVar(&c.path, "config", "", "Scenario file: weighted requests with variables (replaces -url, -method, -header and -body-file)")
	flags.IntVar(&c.print, "print", 0, "With -config, print this many requests as they would be sent, and exit without sending")
	return c
}

// config loads -config, if given. Flags in replaced cannot be combined with it.
func (c *configFlags) config(flags *flag.FlagSet, replaced ...string) (*scenario.Spec, scenario.LoadSettings, error) {
	if c.print < 0 {
		return nil, scenario.LoadSettings{}, fmt.Errorf("-print must not be negative")
	}
	if c.path == "" {
		if c.print > 0 {
			return nil, scenario.LoadSettings{}, fmt.Errorf("-print needs -config")
		}
		return nil, scenario.LoadSettings{}, nil
	}
	var conflicts []string
	for _, name := range replaced {
		if flagSet(flags, name) {
			conflicts = append(conflicts, "-"+name)
		}
	}
	if len(conflicts) > 0 {
		return nil, scenario.LoadSettings{}, fmt.Errorf("-config cannot be combined with %s; the scenario file sets the target and requests", strings.Join(conflicts, ", "))
	}
	return scenario.Load(c.path, nil, worker.MaxRequestBodyBytes)
}

// flagSet reports whether name was given on the command line.
func flagSet(flags *flag.FlagSet, name string) bool {
	found := false
	flags.Visit(func(f *flag.Flag) { found = found || f.Name == name })
	return found
}

// encodeScenario is spec as sent to workers.
func encodeScenario(spec *scenario.Spec) ([]byte, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	if len(data) > worker.MaxCommandBytes-64<<10 {
		return nil, fmt.Errorf("scenario is %d MiB once encoded for workers; the limit is %d MiB", len(data)>>20, (worker.MaxCommandBytes-64<<10)>>20)
	}
	return data, nil
}

// printRequests writes the first n requests worker 1 of workers would send.
func printRequests(w io.Writer, spec *scenario.Spec, workers, n int) error {
	requests, err := worker.RenderScenario(spec, scenario.Position{Worker: 0, Workers: workers}, n)
	if err != nil {
		return err
	}
	for i, r := range requests {
		fmt.Fprintf(w, "### %d: %s\n%s", i+1, r.Name, r.Wire)
		// Requests without a body already end with a blank line.
		if !bytes.HasSuffix(r.Wire, []byte("\r\n\r\n")) {
			fmt.Fprint(w, "\n\n")
		}
	}
	return nil
}
