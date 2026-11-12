// Command pulse-cli provides developer tools for testing and monitoring Pulse clusters.
package main

import (
	"fmt"
	"os"
)

func printUsage() {
	fmt.Println(`Pulse Platform Developer CLI

Usage:
  pulse-cli <command> [arguments]

Commands:
  health  Check health and response latency of a Pulse cluster
  token   Issue a signed test user JWT token
  send    Dispatch a test message via HTTP API
  tail    Tail live binary frames over WebSocket in real-time

Run 'pulse-cli <command> -h' for more details on a specific command.`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "health":
		runHealth(args)
	case "token":
		runToken(args)
	case "send":
		runSend(args)
	case "tail":
		runTail(args)
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command '%s'. Run 'pulse-cli help' for available commands.\n", command)
		os.Exit(1)
	}
}

