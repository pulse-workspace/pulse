package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// runHealth queries cluster health and displays HTTP status.
//
// Business Logic:
// Queries endpoint /health or base URL and reports status, latency and response body.
func runHealth(args []string) {
	fs := flag.NewFlagSet("health", flag.ExitOnError)
	url := fs.String("url", "", "Base URL of Pulse API cluster (e.g. http://localhost:8080)")
	fs.Parse(args)

	if *url == "" {
		fmt.Fprintln(os.Stderr, "Error: --url is required")
		os.Exit(1)
	}

	target := strings.TrimRight(*url, "/") + "/health"
	client := &http.Client{Timeout: 5 * time.Second}

	start := time.Now()
	res, err := client.Get(target)
	dur := time.Since(start)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Cluster health check failed: %v\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	fmt.Printf("Status: %d %s | Latency: %v\nResponse: %s\n", res.StatusCode, http.StatusText(res.StatusCode), dur, string(body))
}

