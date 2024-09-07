package client

import (
	"log"
	"simpleNg/internal/client"
	"simpleNg/pkg/config"
	"simpleNg/pkg/logger"
)

func Run(port int, domain string) error {
	logger.Init()

	cfg := &config.ClientConfig{
		Port:   port,
		Domain: domain,
	}
	if port == 0 {
		cfg.Port = 8080
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
