package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// runSend dispatches a test message via HTTP endpoint.
func runSend(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	apiUrl := fs.String("url", "", "Pulse API endpoint (e.g. http://localhost:8080)")
	token := fs.String("token", "", "User auth JWT token")
	convId := fs.String("conv", "", "Conversation ID (e.g. conv_main)")
	text := fs.String("text", "", "Message text body")
	fs.Parse(args)

	if *apiUrl == "" || *token == "" || *convId == "" || *text == "" {
		fmt.Fprintln(os.Stderr, "Error: --url, --token, --conv, and --text are all mandatory")
		os.Exit(1)
	}

	endpoint := strings.TrimRight(*apiUrl, "/") + "/api/v1/messages/send"
	payload := map[string]any{
		"convID":      *convId,
		"contentType": 101,
		"content":     map[string]any{"text": *text},
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create request: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+*token)

	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to dispatch message: %v\n", err)
		os.Exit(1)
	}
	defer res.Body.Close()

	respBody, _ := io.ReadAll(res.Body)
	fmt.Printf("HTTP %d: %s\n", res.StatusCode, string(respBody))
}

