package config

import (
	"cairnfield/backend/oidc"

	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	Addr         string
	DataDir      string
	DBPath       string
	IndexPath    string
	SessionTTL   time.Duration
	CookieSecure bool
	OIDC         oidc.Config
}

func Load() Config {
	dataDir := getenvAny("/data", "CAIRNFIELD_DATA_DIR", "NOTES_DATA_DIR")
	ttl, err := time.ParseDuration(getenvAny("720h", "CAIRNFIELD_SESSION_TTL", "NOTES_SESSION_TTL"))
	if err != nil || ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	secure, _ := strconv.ParseBool(getenvAny("false", "CAIRNFIELD_COOKIE_SECURE", "NOTES_COOKIE_SECURE"))
	return Config{
		Addr:         getenvAny(":8080", "CAIRNFIELD_ADDR", "NOTES_ADDR"),
		DataDir:      dataDir,
		DBPath:       getenvAny(filepath.Join(dataDir, "cairnfield.db"), "CAIRNFIELD_DB_PATH", "NOTES_DB_PATH"),
		IndexPath:    getenvAny(filepath.Join(dataDir, "bleve"), "CAIRNFIELD_INDEX_PATH", "NOTES_INDEX_PATH"),
		SessionTTL:   ttl,
		CookieSecure: secure,
		OIDC: oidc.Config{
			Issuer:         getenvAny("", "CAIRNFIELD_OIDC_ISSUER", "NOTES_OIDC_ISSUER"),
			ClientID:       getenvAny("", "CAIRNFIELD_OIDC_CLIENT_ID", "NOTES_OIDC_CLIENT_ID"),
			ClientSecret:   getenvAny("", "CAIRNFIELD_OIDC_CLIENT_SECRET", "NOTES_OIDC_CLIENT_SECRET"),
			RedirectURL:    getenvAny("", "CAIRNFIELD_OIDC_REDIRECT_URL", "NOTES_OIDC_REDIRECT_URL"),
			Scopes:         getenvAny("", "CAIRNFIELD_OIDC_SCOPES", "NOTES_OIDC_SCOPES"),
			ProviderName:   getenvAny("", "CAIRNFIELD_OIDC_NAME", "NOTES_OIDC_NAME"),
			AllowedEmails:  oidc.CSVSet(getenvAny("", "CAIRNFIELD_OIDC_ALLOWED_EMAILS", "NOTES_OIDC_ALLOWED_EMAILS")),
			AllowedDomains: oidc.CSVSet(getenvAny("", "CAIRNFIELD_OIDC_ALLOWED_DOMAINS", "NOTES_OIDC_ALLOWED_DOMAINS")),
		}.WithDefaults(),
	}
}

func getenvAny(fallback string, keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return fallback
}
