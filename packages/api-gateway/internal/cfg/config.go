package cfg

import "github.com/caarlos0/env/v11"

type Config struct {
	ServerPort int `env:"SERVER_PORT" envDefault:"8080"`

	RedisAddress  string `env:"REDIS_ADDRESS" envDefault:"redis:6379"`
	RedisPassword  string `env:"REDIS_PASSWORD"`
	RedisDB        int    `env:"REDIS_DB" envDefault:"0"`

	EnvironmentsConfigPath string `env:"ENVIRONMENTS_CONFIG_PATH" envDefault:"/data/environments.yaml"`

	ExecutionDefaultTimeout int `env:"EXECUTION_DEFAULT_TIMEOUT" envDefault:"30"`
	ExecutionMaxTimeout     int `env:"EXECUTION_MAX_TIMEOUT" envDefault:"300"`
	ExecutionSyncMaxTimeout int `env:"EXECUTION_SYNC_MAX_TIMEOUT" envDefault:"30"`

	LimitsMaxFiles      int `env:"LIMITS_MAX_FILES" envDefault:"20"`
	LimitsMaxFileSize   int `env:"LIMITS_MAX_FILE_SIZE" envDefault:"1048576"`
	LimitsMaxTotalSize  int `env:"LIMITS_MAX_TOTAL_SIZE" envDefault:"5242880"`
	LimitsStdoutMaxSize int `env:"LIMITS_STDOUT_MAX_SIZE" envDefault:"2097152"`

	StorageJobTTL      int `env:"STORAGE_JOB_TTL" envDefault:"14400"`
	StorageQueueTimeout int `env:"STORAGE_QUEUE_TIMEOUT" envDefault:"300"`
}

func Parse() (Config, error) {
	var config Config
	err := env.Parse(&config)
	return config, err
}


