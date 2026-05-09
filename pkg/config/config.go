// Package config — config loader berbasis Viper.
// Hierarchy: defaults → file (config.yaml) → env vars (override).
//
// Pakai dari main:
//
//	type AppConfig struct {
//	    Server config.Server
//	    DB     config.Postgres
//	}
//	var cfg AppConfig
//	if err := config.Load("./config", &cfg); err != nil { ... }
package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Server — common server config.
type Server struct {
	GRPCAddr string `mapstructure:"grpc_addr"`
	HTTPAddr string `mapstructure:"http_addr"`
	Env      string `mapstructure:"env"` // dev|staging|prod
}

// Postgres — DB config.
type Postgres struct {
	Host           string `mapstructure:"host"`
	Port           int    `mapstructure:"port"`
	User           string `mapstructure:"user"`
	Password       string `mapstructure:"password"`
	Database       string `mapstructure:"database"`
	SSLMode        string `mapstructure:"sslmode"`
	Schema         string `mapstructure:"schema"`
	MaxConns       int    `mapstructure:"max_conns"`
	MaxIdle        int    `mapstructure:"max_idle"`
	ConnectTimeout string `mapstructure:"connect_timeout"`
}

// DSN — bangun connection string pgx.
func (p Postgres) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s&search_path=%s",
		p.User, p.Password, p.Host, p.Port, p.Database, p.SSLMode, p.Schema,
	)
}

// Redis — cache/lock config.
type Redis struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// NATS — event bus config.
type NATS struct {
	URL    string `mapstructure:"url"`
	Stream string `mapstructure:"stream"`
}

// Telemetry — observability.
type Telemetry struct {
	OTLPEndpoint string  `mapstructure:"otlp_endpoint"`
	ServiceName  string  `mapstructure:"service_name"`
	SamplerRatio float64 `mapstructure:"sampler_ratio"`
}

// Load — generic loader. dir = direktori berisi config.yaml. out = pointer ke struct.
func Load(dir string, out any) error {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(dir)
	v.AddConfigPath(".")

	v.SetEnvPrefix("PP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// File optional — ENV bisa cover semua.
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !asErr(err, &notFound) {
			return fmt.Errorf("config: read: %w", err)
		}
	}

	if err := v.Unmarshal(out); err != nil {
		return fmt.Errorf("config: unmarshal: %w", err)
	}
	return nil
}

func asErr(err error, target any) bool {
	type asAble interface {
		As(target any) bool
	}
	if a, ok := err.(asAble); ok {
		return a.As(target)
	}
	return false
}
