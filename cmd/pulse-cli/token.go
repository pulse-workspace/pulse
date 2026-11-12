package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/pulse-workspace/pulse/pkg/shared/authn"
)

// runToken generates a signed Pulse user JWT for testing.
//
// Business Logic:
// Validates user ID and signing secret, uses authn.Issuer to generate standard
// JWT with TokenUser claims, and prints the token to stdout.
func runToken(args []string) {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	userId := fs.String("user", "", "User ID (e.g. usr_alice)")
	secret := fs.String("secret", "", "JWT signing secret (min 32 chars)")
	platform := fs.String("platform", "CLI", "Platform tag")
	deviceId := fs.String("device", "dev_cli_1", "Device ID")
	fs.Parse(args)

	if *userId == "" {
		fmt.Fprintln(os.Stderr, "Error: --user is required")
		os.Exit(1)
	}
	if *secret == "" || len(*secret) < 32 {
		fmt.Fprintln(os.Stderr, "Error: --secret must be at least 32 characters long")
		os.Exit(1)
	}

	issuer := authn.NewIssuer(*secret, 24*time.Hour, 24*time.Hour, 0)
	tok, _, err := issuer.Issue(authn.TokenUser, *userId, *platform, *deviceId, "", nil, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to issue token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Issued JWT for user '%s':\n%s\n", *userId, tok)
}

