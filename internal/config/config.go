package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	Port          string
	DatabaseURL   string
	CompanyTokens map[string]string // token → company name
}

// Load reads configuration from environment variables.
// Returns an error if any required variable is missing.
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required but not set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	tokens := parseCompanyTokens(os.Getenv("COMPANY_TOKENS"))

	return &Config{
		Port:          port,
		DatabaseURL:   dbURL,
		CompanyTokens: tokens,
	}, nil
}

// parseCompanyTokens parses a comma-separated "token:company" string.
// Example: "abc123:Brookfield Properties,def456:Hines"
func parseCompanyTokens(raw string) map[string]string {
	tokens := make(map[string]string)
	if raw == "" {
		return tokens
	}
	for _, pair := range strings.Split(raw, ",") {
		idx := strings.Index(pair, ":")
		if idx < 1 {
			continue
		}
		token := strings.TrimSpace(pair[:idx])
		company := strings.TrimSpace(pair[idx+1:])
		if token != "" && company != "" {
			tokens[token] = company
		}
	}
	return tokens
}
