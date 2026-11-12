// Package main is the entrypoint for Open-Source Pulse binary.
package main

import "github.com/pulse-workspace/pulse/pkg/app"

var version = "dev"

func main() {
	app.Execute("pulse", version)
}
