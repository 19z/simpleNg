package client

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"simpleNg/internal/client"
	"simpleNg/pkg/config"
	"simpleNg/pkg/logger"
)

func Run(local string, domain string) error {
	logger.Init()
	if local == "" {
		local = "127.0.0.1:8080"
	}
	if strings.HasPrefix(strings.ToLower(local), "file:") || strings.HasPrefix(strings.ToLower(local), "collect:") || strings.HasPrefix(strings.ToLower(local), "ssh:") {
		if _, err := client.NewLocalHandler(local); err != nil {
			return err
		}
	} else {
		host, portNumStr, err := net.SplitHostPort(local)
		if err != nil {
			return fmt.Errorf("invalid local: %s", local)
		}
		if host == "" {
			host = "127.0.0.1"
		}
		portNum, err := strconv.Atoi(portNumStr)
		if err != nil {
			return fmt.Errorf("invalid port: %s", portNumStr)
		}
		if portNum < 1 || portNum > 65535 {
			return fmt.Errorf("invalid port: %d", portNum)
		}
		local = net.JoinHostPort(host, portNumStr)
	}

	cfg := &config.ClientConfig{
		Local:  local,
		Domain: domain,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("Client started, connecting to %s", domain)
	switch {
	case strings.HasPrefix(strings.ToLower(local), "ssh:"):
		log.Printf("Open http(s)://%s in a browser; enter the local SSH account name and password in the terminal", domain)
		log.Printf("With --local=ssh://, the SSH shell starts in the authenticated account's home; press Ctrl+C to stop")
	case strings.HasPrefix(strings.ToLower(local), "collect:"):
		log.Printf("Open http(s)://%s in a browser to upload and download collected files", domain)
	case strings.HasPrefix(strings.ToLower(local), "file:"):
		log.Printf("Open http(s)://%s in a browser to view the published directory", domain)
	default:
		log.Printf("Open http(s)://%s in a browser to reach %s", domain, local)
	}
	return client.RunWithReconnect(ctx, cfg)
}
