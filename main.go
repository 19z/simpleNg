package main

import (
	"flag"
	"log"
	"simpleNg/cmd/client"
	"simpleNg/cmd/server"
)

func main() {
	var port int
	var domain string
	var mode string

	flag.IntVar(&port, "port", 8066, "Port to listen on")
	flag.StringVar(&domain, "domain", "", "Domain to connect to (for client mode)")
	flag.StringVar(&mode, "type", "server", "Mode to run in (server or client)")
	flag.Parse()

	switch mode {
	case "server":
		if err := server.Run(port); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	case "client":
		if domain == "" {
			log.Fatal("Domain is required for client mode")
		}
		if err := client.Run(port, domain); err != nil {
			log.Fatalf("Failed to start client: %v", err)
		}
	default:
		log.Fatalf("Unknown mode: %s", mode)
	}
}
