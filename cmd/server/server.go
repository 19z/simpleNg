package server

import (
	"fmt"
	"log"
	"net/http"
	"simpleNg/internal/server"
	"simpleNg/pkg/config"
	"simpleNg/pkg/logger"
)

func Run(port int) error {
	logger.Init()

	cfg := &config.ServerConfig{
		Port: port,
	}

	serverInstance, err := server.NewServer(cfg)
	if err != nil {
		return err
	}

	log.Printf("Server started, listening on port %d", port)

	http.HandleFunc("/", serverInstance.HandleRequest)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}
