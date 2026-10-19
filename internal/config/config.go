// Package config loads tokentrace's optional config file,
// ~/.tokentrace/config.toml (or $TOKENTRACE_DIR/config.toml):
//
//	[upstreams]
//	anthropic = "https://api.anthropic.com"   # built in, shown for reference
//	gateway   = "https://llm.internal.example.com"
//
//	[defaults]
//	upstream = "gateway"     # used when serve gets no upstream at all
//	port     = 4141
//	project  = "my-app"
//
// Every key is optional and a missing file is the same as an empty one.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strings"
)

// FileName is the config file inside the tokentrace directory.
const FileName = "config.toml"

// Built-in defaults, used when the config file does not override them.
const (
	DefaultPort    = 4141
	DefaultProject = "default"
)

// builtinUpstreams are always available as aliases; [upstreams] may
// override them or add more.
var builtinUpstreams = map[string]string{
	"anthropic": "https://api.anthropic.com",
	"openai":    "https://api.openai.com",
}

// Config is the resolved configuration: built-ins merged with the file.
type Config struct {
	Upstreams map[string]string // alias -> absolute base URL
	Defaults  Defaults
}

// Defaults are the fallback values for serve flags.
type Defaults struct {
	Upstream string // alias or URL; "" means serve must be told one
	Port     int
	Project  string
}

// Default returns the configuration used when no file exists.
func Default() Config {
	ups := make(map[string]string, len(builtinUpstreams))
	for k, v := range builtinUpstreams {
		ups[k] = v
	}
	return Config{Upstreams: ups, Defaults: Defaults{Port: DefaultPort, Project: DefaultProject}}
}

// Load reads the config file at path. A missing file yields Default().
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(string(data))
	if err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Parse builds a Config from the text of a config file.
func Parse(src string) (Config, error) {
	doc, err := parseTOML(src)
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	for table, kv := range doc {
		switch table {
		case "upstreams":
			err = applyUpstreams(cfg.Upstreams, kv)
		case "defaults":
			cfg.Defaults, err = applyDefaults(cfg.Defaults, kv)
		case "":
			if len(kv) > 0 {
				err = fmt.Errorf("key %q must be inside [upstreams] or [defaults]", firstKey(kv))
			}
		default:
			err = fmt.Errorf("unknown table [%s] (want [upstreams] or [defaults])", table)
		}
		if err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func applyUpstreams(dst map[string]string, kv map[string]any) error {
	for alias, v := range kv {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("[upstreams] %s must be a string URL", alias)
		}
		if err := checkURL(s); err != nil {
			return fmt.Errorf("[upstreams] %s: %w", alias, err)
		}
		dst[alias] = s
	}
	return nil
}

func applyDefaults(d Defaults, kv map[string]any) (Defaults, error) {
	for key, v := range kv {
		switch key {
		case "upstream", "project":
			s, ok := v.(string)
			if !ok || s == "" {
				return d, fmt.Errorf("[defaults] %s must be a non-empty string", key)
			}
			if key == "upstream" {
				d.Upstream = s
			} else {
				d.Project = s
			}
		case "port":
			n, ok := v.(int64)
			if !ok || n < 1 || n > 65535 {
				return d, fmt.Errorf("[defaults] port must be an integer in 1-65535, got %v", v)
			}
			d.Port = int(n)
		default:
			return d, fmt.Errorf("unknown key %q in [defaults] (want upstream, port or project)", key)
		}
	}
	return d, nil
}

// ResolveUpstream turns an alias or an absolute URL into a URL. Anything
// containing "://" is taken as a URL; everything else must be an alias.
func (c Config) ResolveUpstream(nameOrURL string) (*url.URL, error) {
	raw := nameOrURL
	if !strings.Contains(nameOrURL, "://") {
		u, ok := c.Upstreams[nameOrURL]
		if !ok {
			return nil, fmt.Errorf("unknown upstream %q: use an alias (%s, or one from [upstreams] in %s) or an absolute URL like https://api.openai.com",
				nameOrURL, strings.Join(c.aliases(), ", "), FileName)
		}
		raw = u
	}
	if err := checkURL(raw); err != nil {
		return nil, err
	}
	return url.Parse(raw)
}

func (c Config) aliases() []string {
	names := make([]string, 0, len(c.Upstreams))
	for k := range c.Upstreams {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func checkURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("parse upstream: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("upstream must be an absolute URL like https://api.openai.com, got %q", s)
	}
	return nil
}

func firstKey(kv map[string]any) string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys[0]
}
