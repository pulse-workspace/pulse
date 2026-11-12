package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"
)

// runTail connects to the WebSocket gateway and tails binary frames.
//
// Business Logic:
// Upgrades WebSocket connection, listens for binary messages, parses the 6-byte Pulse header
// (uint16 frameType + uint32 payloadLength), and prints each frame in real-time.
func runTail(args []string) {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	wsUrl := fs.String("ws", "", "WebSocket gateway endpoint (e.g. ws://localhost:32011/ws)")
	token := fs.String("token", "", "User auth JWT token")
	fs.Parse(args)

	if *wsUrl == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "Error: --ws and --token are required")
		os.Exit(1)
	}

	u, err := url.Parse(*wsUrl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid WebSocket URL: %v\n", err)
		os.Exit(1)
	}

	q := u.Query()
	q.Set("token", *token)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		Subprotocols: []string{"pulse.protocol.v1.msgpack"},
	}

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WebSocket connection failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Printf("Connected to %s. Listening for real-time frames (Ctrl+C to quit)...\n\n", *wsUrl)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-interrupt
		fmt.Println("\nClosing connection...")
		conn.Close()
		os.Exit(0)
	}()

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Connection closed or error: %v\n", err)
			break
		}

		if messageType == websocket.BinaryMessage && len(message) >= 6 {
			frameType := binary.BigEndian.Uint16(message[0:2])
			payloadLen := binary.BigEndian.Uint32(message[2:6])
			payloadBytes := message[6:]

			fmt.Printf("[Frame 0x%04X (%d)] Payload: %d bytes (raw: %q)\n", frameType, frameType, payloadLen, string(payloadBytes))
		} else {
			fmt.Printf("[Text Frame] %s\n", string(message))
		}
	}
}

