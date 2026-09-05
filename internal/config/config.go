// Package config loads config.env and the settings the bot reads from it.
package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds every setting the bot reads at startup.
type Config struct {
	APIID    int
	APIHash  string
	BotToken string

	Admins    []int64
	AuthChats []int64

	NZBGetBase     string
	DownloaderName string

	NZBHydraAPI    string
	NZBHydraAPIURL string
	NZBHydraURL    string

	SearchResultsPerPage int
	AutoRedact           int

	DataDir     string
	LogFile     string
	DBFile      string
	SessionFile string
}

// LoadEnvFile reads a dotenv-style file into the process environment.
//
// Values already present in the environment win, matching python-dotenv's
// default: an operator can override a single setting for one run without
// editing the file.
func LoadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)

		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if _, exists := os.LookupEnv(name); !exists {
			if err := os.Setenv(name, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// Load reads the configuration, failing on any missing required setting.
func Load() (*Config, error) {
	cfg := &Config{}

	// Checked before parsing: reporting "API_ID Not Found" is more useful than
	// a parse error on an empty string.
	var missing []string
	required := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			missing = append(missing, name)
		}
		return value
	}

	apiID := required("API_ID")
	cfg.APIHash = required("API_HASH")
	cfg.BotToken = required("BOT_TOKEN")
	admins := required("ADMINS")
	authChats := required("AUTH_CHATS")
	cfg.NZBGetBase = required("NZBGET_BASE")

	if len(missing) > 0 {
		return nil, fmt.Errorf("%s Not Found", strings.Join(missing, ", "))
	}

	id, err := strconv.Atoi(apiID)
	if err != nil {
		return nil, fmt.Errorf("API_ID is not a number: %w", err)
	}
	cfg.APIID = id

	if cfg.Admins, err = parseIDList("ADMINS", admins); err != nil {
		return nil, err
	}
	if cfg.AuthChats, err = parseIDList("AUTH_CHATS", authChats); err != nil {
		return nil, err
	}

	cfg.DownloaderName = envOr("DOWNLOADER_NAME", "NZBGet")
	cfg.NZBHydraAPI = os.Getenv("NZBHYDRA_API")
	cfg.NZBHydraAPIURL = os.Getenv("NZBHYDRA_API_URL")
	cfg.NZBHydraURL = os.Getenv("NZBHYDRA_URL")
	// Optional, but when set it has to be a URL a request can be built on:
	// a bare host or a relative path fails every /search with a protocol
	// error that says nothing about the setting.
	if cfg.NZBHydraAPIURL != "" {
		parsed, err := url.Parse(cfg.NZBHydraAPIURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("NZBHYDRA_API_URL must be an absolute URL such as https://example.com/api")
		}
	}

	cfg.SearchResultsPerPage = max(1, envInt("SEARCH_RESULTS_PER_PAGE", 5))
	cfg.AutoRedact = max(0, envInt("AUTOREDACT", 0))

	// Defaults to the working directory, which is where config.env is already
	// read from. Set DATA_DIR to keep the log, database and session somewhere
	// fixed when the bot is started from elsewhere.
	dataDir, err := filepath.Abs(expandHome(envOr("DATA_DIR", ".")))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	cfg.DataDir = dataDir
	// logs/<binary>.log, the same shape the sibling clone bot uses, so a group
	// running both gets two log uploads it can tell apart by name.
	cfg.LogFile = filepath.Join(dataDir, "logs", "nzbgetbot.log")
	cfg.DBFile = filepath.Join(dataDir, "bot.db")
	cfg.SessionFile = filepath.Join(dataDir, "session.json")

	return cfg, nil
}

func parseIDList(name, raw string) ([]int64, error) {
	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("%s is not a JSON list of IDs: %w", name, err)
	}
	return ids, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// envInt falls back to the default on anything unparseable rather than
// refusing to start over a malformed optional setting.
func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}
