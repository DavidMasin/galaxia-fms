// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package main

import (
	"flag"
	"github.com/DavidMasin/galaxia-fms/devicecli"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/gamecli"
	"github.com/DavidMasin/galaxia-fms/network"
	"github.com/DavidMasin/galaxia-fms/web"
	"log"
	"os"
)

const defaultEventDbPath = "./event.db"
const defaultHttpPort = 8080

// Main entry point for the application.
func main() {
	// "galaxia-fms game ..." works on game definition files and never starts the field.
	if len(os.Args) > 1 && os.Args[1] == "game" {
		os.Exit(gamecli.Run(os.Args[2:], os.Stdout, os.Stderr))
	}
	// "galaxia-fms device ..." pretends to be a field device of a running FMS, from a terminal.
	if len(os.Args) > 1 && os.Args[1] == "device" {
		os.Exit(devicecli.Run(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}

	flag.BoolVar(&network.DevMode, "dev", false, "Bind driver station listeners to all IP addresses for development")
	flag.StringVar(
		&network.ServerIpAddress, "server-ip", network.DefaultServerIpAddress,
		"IP address the driver station listeners bind to (ignored with -dev)",
	)
	httpPort := flag.Int("port", defaultHttpPort, "Port the web interface is served on")
	eventDbPath := flag.String("db", defaultEventDbPath, "Path of the event database file")
	flag.Parse()

	arena, err := field.NewArena(*eventDbPath)
	if err != nil {
		log.Fatalln("Error during startup: ", err)
	}

	// Start the web server in a separate goroutine.
	web := web.NewWeb(arena)
	if err = web.RegisterFieldActionHandlers(); err != nil {
		log.Fatalln("Error during startup: ", err)
	}
	go web.ServeWebInterface(*httpPort)

	// Run the arena state machine in the main thread.
	arena.Run()
}
