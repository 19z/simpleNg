package client

import (
	"fmt"
	"log"
	"net/http"
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

	clientInstance, err := client.NewClient(cfg)
	if err != nil {
		return err
	}

	log.Printf("Client started, connecting to %s", domain)

	http.HandleFunc("/", clientInstance.HandleRequest)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}
