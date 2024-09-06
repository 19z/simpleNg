package config

type ServerConfig struct {
	Port int
}

type ClientConfig struct {
	Port   int
	Domain string
}
