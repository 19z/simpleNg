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
	if port == 0 {
		cfg.Port = 8066
	}

	serverInstance, err := server.NewServer(cfg)
	if err != nil {
		return err
	}

	go serverInstance.MessageHandler()
	http.HandleFunc("/", serverInstance.HandleRequest)
	log.Printf("Server started, listening on port %d", cfg.Port)
	return http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), nil)
}
