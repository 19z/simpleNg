package client

import (
	"fmt"
	"log"
	"net"
	"simpleNg/internal/client"
	"simpleNg/pkg/config"
	"simpleNg/pkg/logger"
	"strconv"
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
	if portNum < 1 || portNum > 65525 {
		return fmt.Errorf("invalid port: %d", portNum)
	}

	cfg := &config.ClientConfig{
		Local:  local,
		Domain: domain,
	}
	clientInstance, err := client.NewClient(cfg)
	if err != nil {
		return err
	}

	log.Printf("Client started, connecting to %s", domain)

	err = clientInstance.MessageHandler()
	if err != nil {
		return err
	}
	return nil
}
