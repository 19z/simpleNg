package client

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
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

	cfg := &config.ClientConfig{
		Local:  net.JoinHostPort(host, portNumStr),
		Domain: domain,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("Client started, connecting to %s", domain)
	return client.RunWithReconnect(ctx, cfg)
}
