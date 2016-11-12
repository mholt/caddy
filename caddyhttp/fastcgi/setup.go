package fastcgi

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mholt/caddy"
	"github.com/mholt/caddy/caddyhttp/httpserver"
)

func init() {
	caddy.RegisterPlugin("fastcgi", caddy.Plugin{
		ServerType: "http",
		Action:     setup,
	})
}

// setup configures a new FastCGI middleware instance.
func setup(c *caddy.Controller) error {
	cfg := httpserver.GetConfig(c)
	absRoot, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}

	rules, err := fastcgiParse(c)
	if err != nil {
		return err
	}

	cfg.AddMiddleware(func(next httpserver.Handler) httpserver.Handler {
		return Handler{
			Next:            next,
			Rules:           rules,
			Root:            cfg.Root,
			AbsRoot:         absRoot,
			FileSys:         http.Dir(cfg.Root),
			SoftwareName:    caddy.AppName,
			SoftwareVersion: caddy.AppVersion,
			ServerName:      cfg.Addr.Host,
			ServerPort:      cfg.Addr.Port,
		}
	})

	return nil
}

func fastcgiParse(c *caddy.Controller) ([]Rule, error) {
	var rules []Rule

	for c.Next() {
		var rule Rule

		args := c.RemainingArgs()

		switch len(args) {
		case 0:
			return rules, c.ArgErr()
		case 1:
			rule.Path = "/"
			rule.Address = args[0]
		case 2:
			rule.Path = args[0]
			rule.Address = args[1]
		case 3:
			rule.Path = args[0]
			rule.Address = args[1]
			err := fastcgiPreset(args[2], &rule)
			if err != nil {
				return rules, c.Err("Invalid fastcgi rule preset '" + args[2] + "'")
			}
		}

		persistent := false
		var err error
		var pool int
		var timeout time.Duration

		for c.NextBlock() {
			switch c.Val() {
			case "ext":
				if !c.NextArg() {
					return rules, c.ArgErr()
				}
				rule.Ext = c.Val()
			case "split":
				if !c.NextArg() {
					return rules, c.ArgErr()
				}
				rule.SplitPath = c.Val()
			case "index":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return rules, c.ArgErr()
				}
				rule.IndexFiles = args
			case "env":
				envArgs := c.RemainingArgs()
				if len(envArgs) < 2 {
					return rules, c.ArgErr()
				}
				rule.EnvVars = append(rule.EnvVars, [2]string{envArgs[0], envArgs[1]})
			case "except":
				ignoredPaths := c.RemainingArgs()
				if len(ignoredPaths) == 0 {
					return rules, c.ArgErr()
				}
				rule.IgnoredSubPaths = ignoredPaths
			case "pool":
				if !c.NextArg() {
					return rules, c.ArgErr()
				}
				pool, err = strconv.Atoi(c.Val())
				if err != nil {
					return rules, err
				}
				if pool >= 0 {
					persistent = true
				} else {
					return rules, c.Errf("positive integer expected, found %d", pool)
				}
			case "connect_timeout":
				if !c.NextArg() {
					return rules, c.ArgErr()
				}
				timeout, err = time.ParseDuration(c.Val())
				if err != nil {
					return rules, err
				}
			case "read_timeout":
				if !c.NextArg() {
					return rules, c.ArgErr()
				}
				readTimeout, err := time.ParseDuration(c.Val())
				if err != nil {
					return rules, err
				}
				rule.ReadTimeout = readTimeout
			}
		}

		network, address := parseAddress(rule.Address)
		if persistent == true {
			rule.dialer = &persistentDialer{
				size:    pool,
				network: network,
				address: address,
				timeout: timeout,
			}
		} else {
			rule.dialer = basicDialer{
				network: network,
				address: address,
				timeout: timeout,
			}
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

// fastcgiPreset configures rule according to name. It returns an error if
// name is not a recognized preset name.
func fastcgiPreset(name string, rule *Rule) error {
	switch name {
	case "php":
		rule.Ext = ".php"
		rule.SplitPath = ".php"
		rule.IndexFiles = []string{"index.php"}
	default:
		return errors.New(name + " is not a valid preset name")
	}
	return nil
}
