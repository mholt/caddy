package push

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/mholt/caddy"
	"github.com/mholt/caddy/caddyhttp/httpserver"
)

func init() {
	caddy.RegisterPlugin("push", caddy.Plugin{
		ServerType: "http",
		Action:     setup,
	})
}

// ErrNotSupported is returned when push directive is not available
var ErrNotSupported = errors.New("push directive is available when build on golang 1.8")

var invalidFormat = errors.New("push directive has invalid format, expected push path [resources, ]")

// setup configures a new Push middleware
func setup(c *caddy.Controller) error {

	if !http2PushSupported() {
		return ErrNotSupported
	}

	rules, err := parsePushRules(c)

	if err != nil {
		return err
	}

	httpserver.GetConfig(c).AddMiddleware(func(next httpserver.Handler) httpserver.Handler {
		return Middleware{Next: next, Rules: rules}
	})

	return nil
}

func parsePushRules(c *caddy.Controller) ([]Rule, error) {
	var rules = make(map[string]*Rule)
	var emptyRules = []Rule{}

	for c.NextLine() {
		if !c.NextArg() {
			return emptyRules, c.ArgErr()
		}

		path := c.Val()
		args := c.RemainingArgs()

		if len(args) < 1 {
			return emptyRules, invalidFormat
		}

		var rule *Rule

		if existingRule, ok := rules[path]; ok {
			rule = existingRule
		} else {
			rule = new(Rule)
			rule.Path = path
			rules[rule.Path] = rule
		}

		for i := 0; i < len(args); i++ {
			rule.Resources = append(rule.Resources, Resource{
				Path:   args[i],
				Method: "GET",
				Header: http.Header{},
			})
		}

		for c.NextBlock() {
			switch c.Val() {
			case "method":
				fmt.Println(c.Val())
			}
		}
	}

	var returnRules []Rule

	for _, rule := range rules {
		returnRules = append(returnRules, *rule)
	}

	return returnRules, nil
}
