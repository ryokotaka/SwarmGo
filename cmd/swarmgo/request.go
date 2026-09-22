package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ryokotaka/SwarmGo/internal/worker"
)

type requestFlags struct {
	method   string
	bodyFile string
	headers  headerFlags
}

func addRequestFlags(flags *flag.FlagSet) *requestFlags {
	request := &requestFlags{}
	flags.StringVar(&request.method, "method", http.MethodGet, "HTTP method")
	flags.StringVar(&request.bodyFile, "body-file", "", "Read the request body from this file (maximum 1 MiB)")
	flags.Var(&request.headers, "header", "Request header 'Name: value'; repeat for different names")
	return request
}

func (f *requestFlags) load() (worker.RequestOptions, error) {
	options := worker.RequestOptions{Method: f.method, Headers: map[string]string(f.headers)}
	if f.bodyFile != "" {
		file, err := os.Open(f.bodyFile)
		if err != nil {
			return options, fmt.Errorf("open body file: %w", err)
		}
		defer file.Close()
		options.Body, err = io.ReadAll(io.LimitReader(file, worker.MaxRequestBodyBytes+1))
		if err != nil {
			return options, fmt.Errorf("read body file: %w", err)
		}
	}
	if err := worker.ValidateRequestOptions(options); err != nil {
		return options, err
	}
	return options, nil
}

type headerFlags map[string]string

func (h *headerFlags) String() string { return "" }

func (h *headerFlags) Set(value string) error {
	name, content, ok := strings.Cut(value, ":")
	if !ok {
		return fmt.Errorf("header must use 'Name: value'")
	}
	name = http.CanonicalHeaderKey(strings.TrimSpace(name))
	content = strings.TrimSpace(content)
	if err := worker.ValidateRequestOptions(worker.RequestOptions{Headers: map[string]string{name: content}}); err != nil {
		return err
	}
	if *h == nil {
		*h = make(headerFlags)
	}
	(*h)[name] = content
	return nil
}
