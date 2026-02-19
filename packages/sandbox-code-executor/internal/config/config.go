package config

import (
	"os"
	"strconv"
)

// Defaults from TZ
const (
	DefaultAPIPort        = 8080
	DefaultWorkerPoolSize = 10
	DefaultTimeout        = 30
	MaxTimeout            = 300
	QueueTimeout          = 300
	MemoryLimit           = 268435456   // 256 MB
	StdoutMaxSize         = 2097152     // 2 MB
	ResultTTLHours        = 4
	MaxFilesPerJob        = 20
	MaxFileSize           = 1024 * 1024  // 1 MB
	MaxTotalFilesSize     = 5 * 1024 * 1024 // 5 MB
)

type Config struct {
	APIPort        int
	WorkerPoolSize int
	DefaultTimeout int
	MaxTimeout     int
	QueueTimeout   int
	MemoryLimit    int64
	StdoutMaxSize  int
	RedisURL       string
	PistonURL      string
	Debug          bool
}

func FromEnv() *Config {
	return &Config{
		APIPort:        intEnv("API_PORT", DefaultAPIPort),
		WorkerPoolSize: intEnv("WORKER_POOL_SIZE", DefaultWorkerPoolSize),
		DefaultTimeout: intEnv("DEFAULT_TIMEOUT", DefaultTimeout),
		MaxTimeout:     intEnv("MAX_TIMEOUT", MaxTimeout),
		QueueTimeout:   intEnv("QUEUE_TIMEOUT", QueueTimeout),
		MemoryLimit:    int64Env("MEMORY_LIMIT", MemoryLimit),
		StdoutMaxSize:  intEnv("STDOUT_MAX_SIZE", StdoutMaxSize),
		RedisURL:       strEnv("REDIS_URL", "redis://localhost:6379"),
		PistonURL:      strEnv("PISTON_URL", "http://localhost:2000"),
		Debug:          strEnv("DEBUG", "0") == "1",
	}
}

func strEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func int64Env(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
