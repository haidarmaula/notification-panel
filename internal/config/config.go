package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	APIKey        string
	AccessSecret  string
	RefreshSecret string
}

func Load() *Config {
	godotenv.Load()

	cfg := &Config{
		APIKey:        os.Getenv("API_KEY"),
		AccessSecret:  os.Getenv("ACCESS_SECRET"),
		RefreshSecret: os.Getenv("REFRESH_SECRET"),
	}

	if cfg.APIKey == "" {
		log.Fatal("API_KEY is required")
	}

	if cfg.AccessSecret == "" {
		log.Fatal("ACCESS_SECRET is required")
	}

	if cfg.RefreshSecret == "" {
		log.Fatal("REFRESH_SECRET is required")
	}

	return cfg
}
