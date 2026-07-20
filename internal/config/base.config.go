package config

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
)

type BaseConfig struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string
}

func LoadBaseConfig() *BaseConfig {
	godotenv.Load()

	cfg := &BaseConfig{
		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     os.Getenv("DB_PORT"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBName:     os.Getenv("DB_NAME"),
		DBSSLMode:  os.Getenv("DB_SSLMODE"),
	}

	if cfg.DBHost == "" {
		log.Fatal("DB_HOST is required")
	}

	if cfg.DBPort == "" {
		log.Fatal("DB_PORT is required")
	}

	if cfg.DBUser == "" {
		log.Fatal("DB_USER is required")
	}

	if cfg.DBPassword == "" {
		log.Fatal("DB_PASSWORD is required")
	}

	if cfg.DBName == "" {
		log.Fatal("DB_NAME is required")
	}

	if cfg.DBSSLMode == "" {
		log.Fatal("DB_SSLMODE is required")
	}

	return cfg
}

func (c *BaseConfig) GetDatabaseURL() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser,
		c.DBPassword,
		c.DBHost,
		c.DBPort,
		c.DBName,
		c.DBSSLMode,
	)
}
