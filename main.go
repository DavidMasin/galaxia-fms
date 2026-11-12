// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package main

import (
	"flag"
	"github.com/DavidMasin/galaxia-fms/field"
	"github.com/DavidMasin/galaxia-fms/network"
	"github.com/DavidMasin/galaxia-fms/web"
	"log"
)

const eventDbPath = "./event.db"
const httpPort = 8080

// Main entry point for the application.
func main() {
	flag.BoolVar(&network.DevMode, "dev", false, "Bind driver station listeners to all IP addresses for development")
	flag.Parse()

	arena, err := field.NewArena(eventDbPath)
	if err != nil {
		log.Fatalln("Error during startup: ", err)
	}

	// Start the web server in a separate goroutine.
	web := web.NewWeb(arena)
	go web.ServeWebInterface(httpPort)

	// Run the arena state machine in the main thread.
	arena.Run()
}
