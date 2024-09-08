package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"simpleNg/cmd/client"
	"simpleNg/cmd/server"
)

func main() {
	var port int
	var domain string
	var mode string
	var local string

	// 解析命令行参数
	flag.IntVar(&port, "port", 8066, "Port to listen on (for server mode)")
	flag.StringVar(&mode, "type", "client", "Mode to run in (server or client)")
	flag.StringVar(&domain, "domain", "", "Domain to connect to (for client mode)")
	flag.StringVar(&local, "local", "", "Local domain:point or ip:port to listen on (for client mode)，like localhost:8080")

	// 设置自定义的 Usage
	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		_, _ = fmt.Fprintf(os.Stderr, "  -port int\n")
		_, _ = fmt.Fprintf(os.Stderr, "     Port to listen on (for server mode, default:8066)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  -domain string\n")
		_, _ = fmt.Fprintf(os.Stderr, "     Domain to connect to (for client mode)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  -type string\n")
		_, _ = fmt.Fprintf(os.Stderr, "     Mode to run in (server or client)\n")
		_, _ = fmt.Fprintf(os.Stderr, "  -local string\n")
		_, _ = fmt.Fprintf(os.Stderr, "     Local domain:point or ip:port to listen on (for client mode), like localhost:8080\n")
		_, _ = fmt.Fprintf(os.Stderr, "\n")
		_, _ = fmt.Fprintf(os.Stderr, "Examples:\n")
		_, _ = fmt.Fprintf(os.Stderr, "  Run in server mode:\n")
		_, _ = fmt.Fprintf(os.Stderr, "    %s -type=server -port=8066\n", os.Args[0])
		_, _ = fmt.Fprintf(os.Stderr, "  Run in client mode:\n")
		_, _ = fmt.Fprintf(os.Stderr, "    %s -type=client -domain=testprefix.example.com:8066 -local=localhost:8080\n", os.Args[0])
		_, _ = fmt.Fprintf(os.Stderr, "\n")
	}

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
		if err := client.Run(local, domain); err != nil {
			log.Fatalf("Failed to start client: %v", err)
		}
	default:
		log.Fatalf("Unknown mode: %s", mode)
	}
}
