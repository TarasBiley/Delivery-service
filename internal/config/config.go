package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL string
	RedisAddr   string
	MongoURI    string
	MongoDB     string
	RabbitMQURL string

	HTTPAddr     string
	CBRURL       string
	DebugMode    bool
	CookieSecure bool

	HTTPReadHeaderTimeout time.Duration
	HTTPReadTimeout       time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration
	HTTPShutdownTimeout   time.Duration
}

func Load() (Config, error) {
	debugMode, err := strconv.ParseBool(
		getEnv("DEBUG_MODE", "false"),
	)
	if err != nil {
		return Config{}, fmt.Errorf(
			"parse DEBUG_MODE: %w",
			err,
		)
	}
	cookieSecure, err := strconv.ParseBool(
		getEnv("COOKIE_SECURE", "false"),
	)
	if err != nil {
		return Config{}, fmt.Errorf(
			"parse COOKIE_SECURE: %w",
			err,
		)
	}

	readHeaderTimeout, err := getDuration(
		"HTTP_READ_HEADER_TIMEOUT",
		"5s",
	)
	if err != nil {
		return Config{}, err
	}

	readTimeout, err := getDuration(
		"HTTP_READ_TIMEOUT",
		"10s",
	)
	if err != nil {
		return Config{}, err
	}

	writeTimeout, err := getDuration(
		"HTTP_WRITE_TIMEOUT",
		"15s",
	)
	if err != nil {
		return Config{}, err
	}

	idleTimeout, err := getDuration(
		"HTTP_IDLE_TIMEOUT",
		"60s",
	)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := getDuration(
		"HTTP_SHUTDOWN_TIMEOUT",
		"5s",
	)
	if err != nil {
		return Config{}, err
	}

	return Config{
		DatabaseURL: getEnv(
			"DATABASE_URL",
			"postgres://delivery:delivery@localhost:5437/delivery?sslmode=disable",
		),

		RedisAddr: getEnv(
			"REDIS_ADDR",
			"localhost:6380",
		),

		MongoURI: getEnv(
			"MONGO_URI",
			"mongodb://delivery:delivery@localhost:27017/delivery?authSource=admin",
		),

		MongoDB: getEnv(
			"MONGO_DB",
			"delivery",
		),

		RabbitMQURL: getEnv(
			"RABBITMQ_URL",
			"amqp://delivery:delivery@localhost:5672/",
		),

		HTTPAddr: getEnv(
			"HTTP_ADDR",
			":8080",
		),

		CBRURL: getEnv(
			"CBR_URL",
			"https://www.cbr-xml-daily.ru/daily_json.js",
		),

		DebugMode:    debugMode,
		CookieSecure: cookieSecure,

		HTTPReadHeaderTimeout: readHeaderTimeout,
		HTTPReadTimeout:       readTimeout,
		HTTPWriteTimeout:      writeTimeout,
		HTTPIdleTimeout:       idleTimeout,
		HTTPShutdownTimeout:   shutdownTimeout,
	}, nil
}

func getEnv(
	key string,
	fallback string,
) string {
	value := os.Getenv(key)

	if value == "" {
		return fallback
	}

	return value
}

func getDuration(
	key string,
	fallback string,
) (time.Duration, error) {
	value := getEnv(key, fallback)

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf(
			"parse %s: %w",
			key,
			err,
		)
	}

	if duration <= 0 {
		return 0, fmt.Errorf(
			"%s must be greater than 0",
			key,
		)
	}

	return duration, nil
}
