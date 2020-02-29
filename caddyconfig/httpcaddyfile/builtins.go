// Copyright 2015 Matthew Holt and The Caddy Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package httpcaddyfile

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"reflect"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddytls"
	"go.uber.org/zap/zapcore"
)

func init() {
	RegisterDirective("bind", parseBind)
	RegisterDirective("root", parseRoot) // TODO: isn't this a handler directive?
	RegisterDirective("tls", parseTLS)
	RegisterHandlerDirective("redir", parseRedir)
	RegisterHandlerDirective("respond", parseRespond)
	RegisterHandlerDirective("route", parseRoute)
	RegisterHandlerDirective("handle", parseHandle)
	RegisterDirective("handle_errors", parseHandleErrors)
	RegisterDirective("log", parseLog)
}

// parseBind parses the bind directive. Syntax:
//
//     bind <addresses...>
//
func parseBind(h Helper) ([]ConfigValue, error) {
	var lnHosts []string
	for h.Next() {
		lnHosts = append(lnHosts, h.RemainingArgs()...)
	}
	return h.NewBindAddresses(lnHosts), nil
}

// parseRoot parses the root directive. Syntax:
//
//     root [<matcher>] <path>
//
func parseRoot(h Helper) ([]ConfigValue, error) {
	if !h.Next() {
		return nil, h.ArgErr()
	}

	matcherSet, ok, err := h.MatcherToken()
	if err != nil {
		return nil, err
	}
	if !ok {
		// no matcher token; oops
		h.Dispenser.Prev()
	}

	if !h.NextArg() {
		return nil, h.ArgErr()
	}
	root := h.Val()
	if h.NextArg() {
		return nil, h.ArgErr()
	}

	varsHandler := caddyhttp.VarsMiddleware{"root": root}
	route := caddyhttp.Route{
		HandlersRaw: []json.RawMessage{
			caddyconfig.JSONModuleObject(varsHandler, "handler", "vars", nil),
		},
	}
	if matcherSet != nil {
		route.MatcherSetsRaw = []caddy.ModuleMap{matcherSet}
	}

	return []ConfigValue{{Class: "route", Value: route}}, nil
}

// parseTLS parses the tls directive. Syntax:
//
//     tls [<email>]|[<cert_file> <key_file>] {
//         protocols <min> [<max>]
//         ciphers   <cipher_suites...>
//         curves    <curves...>
//         alpn      <values...>
//         load      <paths...>
//         ca        <acme_ca_endpoint>
//         dns       <provider_name>
//     }
//
func parseTLS(h Helper) ([]ConfigValue, error) {
	var configVals []ConfigValue

	var cp *caddytls.ConnectionPolicy
	var fileLoader caddytls.FileLoader
	var folderLoader caddytls.FolderLoader
	var mgr caddytls.ACMEManagerMaker

	// fill in global defaults, if configured
	if email := h.Option("email"); email != nil {
		mgr.Email = email.(string)
	}
	if acmeCA := h.Option("acme_ca"); acmeCA != nil {
		mgr.CA = acmeCA.(string)
	}
	if caPemFile := h.Option("acme_ca_root"); caPemFile != nil {
		mgr.TrustedRootsPEMFiles = append(mgr.TrustedRootsPEMFiles, caPemFile.(string))
	}

	for h.Next() {
		// file certificate loader
		firstLine := h.RemainingArgs()
		switch len(firstLine) {
		case 0:
		case 1:
			if !strings.Contains(firstLine[0], "@") {
				return nil, h.Err("single argument must be an email address")
			}
			mgr.Email = firstLine[0]
		case 2:
			certFilename := firstLine[0]
			keyFilename := firstLine[1]

			// tag this certificate so if multiple certs match, specifically
			// this one that the user has provided will be used, see #2588:
			// https://github.com/caddyserver/caddy/issues/2588 ... but we
			// must be careful about how we do this; being careless will
			// lead to failed handshakes

			// we need to remember which cert files we've seen, since we
			// must load each cert only once; otherwise, they each get a
			// different tag... since a cert loaded twice has the same
			// bytes, it will overwrite the first one in the cache, and
			// only the last cert (and its tag) will survive, so a any conn
			// policy that is looking for any tag but the last one to be
			// loaded won't find it, and TLS handshakes will fail (see end)
			// of issue #3004)
			tag, ok := tlsCertTags[certFilename]
			if !ok {
				// haven't seen this cert file yet, let's give it a tag
				// and add a loader for it
				tag = fmt.Sprintf("cert%d", len(tlsCertTags))
				// remember this for next time we see this cert file
				tlsCertTags[certFilename] = tag
			}

			fileLoader = append(fileLoader, caddytls.CertKeyFilePair{
				Certificate: certFilename,
				Key:         keyFilename,
				Tags:        []string{tag},
			})

			certSelector := caddytls.CustomCertSelectionPolicy{Tag: tag}
			if cp == nil {
				cp = new(caddytls.ConnectionPolicy)
			}

			cp.CertSelection = caddyconfig.JSONModuleObject(certSelector, "policy", "custom", h.warnings)
		default:
			return nil, h.ArgErr()
		}

		var hasBlock bool
		for h.NextBlock(0) {
			hasBlock = true

			switch h.Val() {
			// connection policy
			case "protocols":
				args := h.RemainingArgs()
				if len(args) == 0 {
					return nil, h.SyntaxErr("one or two protocols")
				}
				if len(args) > 0 {
					if _, ok := caddytls.SupportedProtocols[args[0]]; !ok {
						return nil, h.Errf("Wrong protocol name or protocol not supported: '%s'", args[0])
					}
					if cp == nil {
						cp = new(caddytls.ConnectionPolicy)
					}
					cp.ProtocolMin = args[0]
				}
				if len(args) > 1 {
					if _, ok := caddytls.SupportedProtocols[args[1]]; !ok {
						return nil, h.Errf("Wrong protocol name or protocol not supported: '%s'", args[1])
					}
					if cp == nil {
						cp = new(caddytls.ConnectionPolicy)
					}
					cp.ProtocolMax = args[1]
				}
			case "ciphers":
				for h.NextArg() {
					if _, ok := caddytls.SupportedCipherSuites[h.Val()]; !ok {
						return nil, h.Errf("Wrong cipher suite name or cipher suite not supported: '%s'", h.Val())
					}
					if cp == nil {
						cp = new(caddytls.ConnectionPolicy)
					}
					cp.CipherSuites = append(cp.CipherSuites, h.Val())
				}
			case "curves":
				for h.NextArg() {
					if _, ok := caddytls.SupportedCurves[h.Val()]; !ok {
						return nil, h.Errf("Wrong curve name or curve not supported: '%s'", h.Val())
					}
					if cp == nil {
						cp = new(caddytls.ConnectionPolicy)
					}
					cp.Curves = append(cp.Curves, h.Val())
				}
			case "alpn":
				args := h.RemainingArgs()
				if len(args) == 0 {
					return nil, h.ArgErr()
				}
				if cp == nil {
					cp = new(caddytls.ConnectionPolicy)
				}
				cp.ALPN = args

			// certificate folder loader
			case "load":
				folderLoader = append(folderLoader, h.RemainingArgs()...)

			// automation policy
			case "ca":
				arg := h.RemainingArgs()
				if len(arg) != 1 {
					return nil, h.ArgErr()
				}
				mgr.CA = arg[0]

			// DNS provider for ACME DNS challenge
			case "dns":
				if !h.Next() {
					return nil, h.ArgErr()
				}
				provName := h.Val()
				if mgr.Challenges == nil {
					mgr.Challenges = new(caddytls.ChallengesConfig)
				}
				dnsProvModule, err := caddy.GetModule("tls.dns." + provName)
				if err != nil {
					return nil, h.Errf("getting DNS provider module named '%s': %v", provName, err)
				}
				mgr.Challenges.DNSRaw = caddyconfig.JSONModuleObject(dnsProvModule.New(), "provider", provName, h.warnings)

			case "ca_root":
				arg := h.RemainingArgs()
				if len(arg) != 1 {
					return nil, h.ArgErr()
				}
				mgr.TrustedRootsPEMFiles = append(mgr.TrustedRootsPEMFiles, arg[0])

			default:
				return nil, h.Errf("unknown subdirective: %s", h.Val())
			}
		}

		// a naked tls directive is not allowed
		if len(firstLine) == 0 && !hasBlock {
			return nil, h.ArgErr()
		}
	}

	// certificate loaders
	if len(fileLoader) > 0 {
		configVals = append(configVals, ConfigValue{
			Class: "tls.certificate_loader",
			Value: fileLoader,
		})
		// ensure server uses HTTPS by setting non-nil conn policy
		if cp == nil {
			cp = new(caddytls.ConnectionPolicy)
		}
	}
	if len(folderLoader) > 0 {
		configVals = append(configVals, ConfigValue{
			Class: "tls.certificate_loader",
			Value: folderLoader,
		})
		// ensure server uses HTTPS by setting non-nil conn policy
		if cp == nil {
			cp = new(caddytls.ConnectionPolicy)
		}
	}

	// connection policy
	if cp != nil {
		configVals = append(configVals, ConfigValue{
			Class: "tls.connection_policy",
			Value: cp,
		})
	}

	// automation policy
	if !reflect.DeepEqual(mgr, caddytls.ACMEManagerMaker{}) {
		configVals = append(configVals, ConfigValue{
			Class: "tls.automation_manager",
			Value: mgr,
		})
	}

	return configVals, nil
}

// parseRedir parses the redir directive. Syntax:
//
//     redir [<matcher>] <to> [<code>]
//
func parseRedir(h Helper) (caddyhttp.MiddlewareHandler, error) {
	if !h.Next() {
		return nil, h.ArgErr()
	}

	if !h.NextArg() {
		return nil, h.ArgErr()
	}
	to := h.Val()

	var code string
	if h.NextArg() {
		code = h.Val()
	}
	if code == "permanent" {
		code = "301"
	}
	if code == "temporary" || code == "" {
		code = "302"
	}
	var body string
	if code == "html" {
		// Script tag comes first since that will better imitate a redirect in the browser's
		// history, but the meta tag is a fallback for most non-JS clients.
		const metaRedir = `<!DOCTYPE html>
<html>
	<head>
		<title>Redirecting...</title>
		<script>window.location.replace("%s");</script>
		<meta http-equiv="refresh" content="0; URL='%s'">
	</head>
	<body>Redirecting to <a href="%s">%s</a>...</body>
</html>
`
		safeTo := html.EscapeString(to)
		body = fmt.Sprintf(metaRedir, safeTo, safeTo, safeTo, safeTo)
	}

	return caddyhttp.StaticResponse{
		StatusCode: caddyhttp.WeakString(code),
		Headers:    http.Header{"Location": []string{to}},
		Body:       body,
	}, nil
}

// parseRespond parses the respond directive.
func parseRespond(h Helper) (caddyhttp.MiddlewareHandler, error) {
	sr := new(caddyhttp.StaticResponse)
	err := sr.UnmarshalCaddyfile(h.Dispenser)
	if err != nil {
		return nil, err
	}
	return sr, nil
}

// parseRoute parses the route directive.
func parseRoute(h Helper) (caddyhttp.MiddlewareHandler, error) {
	sr := new(caddyhttp.Subroute)

	for h.Next() {
		for nesting := h.Nesting(); h.NextBlock(nesting); {
			dir := h.Val()

			dirFunc, ok := registeredDirectives[dir]
			if !ok {
				return nil, h.Errf("unrecognized directive: %s", dir)
			}

			subHelper := h
			subHelper.Dispenser = h.NewFromNextSegment()

			results, err := dirFunc(subHelper)
			if err != nil {
				return nil, h.Errf("parsing caddyfile tokens for '%s': %v", dir, err)
			}
			for _, result := range results {
				handler, ok := result.Value.(caddyhttp.Route)
				if !ok {
					return nil, h.Errf("%s directive returned something other than an HTTP route: %#v (only handler directives can be used in routes)", dir, result.Value)
				}
				sr.Routes = append(sr.Routes, handler)
			}
		}
	}

	return sr, nil
}

func parseHandle(h Helper) (caddyhttp.MiddlewareHandler, error) {
	return parseSegmentAsSubroute(h)
}

func parseHandleErrors(h Helper) ([]ConfigValue, error) {
	subroute, err := parseSegmentAsSubroute(h)
	if err != nil {
		return nil, err
	}
	return []ConfigValue{
		{
			Class: "error_route",
			Value: subroute,
		},
	}, nil
}

// parseLog parses the log directive. Syntax:
//
//     log {
//         output <writer_module> ...
//         format <encoder_module> ...
//         level  <level>
//     }
//
func parseLog(h Helper) ([]ConfigValue, error) {
	var configValues []ConfigValue
	for h.Next() {
		cl := new(caddy.CustomLog)

		for h.NextBlock(0) {
			switch h.Val() {
			case "output":
				if !h.NextArg() {
					return nil, h.ArgErr()
				}
				moduleName := h.Val()

				// can't use the usual caddyfile.Unmarshaler flow with the
				// standard writers because they are in the caddy package
				// (because they are the default) and implementing that
				// interface there would unfortunately create circular import
				var wo caddy.WriterOpener
				switch moduleName {
				case "stdout":
					wo = caddy.StdoutWriter{}
				case "stderr":
					wo = caddy.StderrWriter{}
				case "discard":
					wo = caddy.DiscardWriter{}
				default:
					mod, err := caddy.GetModule("caddy.logging.writers." + moduleName)
					if err != nil {
						return nil, h.Errf("getting log writer module named '%s': %v", moduleName, err)
					}
					unm, ok := mod.New().(caddyfile.Unmarshaler)
					if !ok {
						return nil, h.Errf("log writer module '%s' is not a Caddyfile unmarshaler", mod)
					}
					err = unm.UnmarshalCaddyfile(h.NewFromNextSegment())
					if err != nil {
						return nil, err
					}
					wo, ok = unm.(caddy.WriterOpener)
					if !ok {
						return nil, h.Errf("module %s is not a WriterOpener", mod)
					}
				}
				cl.WriterRaw = caddyconfig.JSONModuleObject(wo, "output", moduleName, h.warnings)

			case "format":
				if !h.NextArg() {
					return nil, h.ArgErr()
				}
				moduleName := h.Val()
				mod, err := caddy.GetModule("caddy.logging.encoders." + moduleName)
				if err != nil {
					return nil, h.Errf("getting log encoder module named '%s': %v", moduleName, err)
				}
				unm, ok := mod.New().(caddyfile.Unmarshaler)
				if !ok {
					return nil, h.Errf("log encoder module '%s' is not a Caddyfile unmarshaler", mod)
				}
				err = unm.UnmarshalCaddyfile(h.NewFromNextSegment())
				if err != nil {
					return nil, err
				}
				enc, ok := unm.(zapcore.Encoder)
				if !ok {
					return nil, h.Errf("module %s is not a zapcore.Encoder", mod)
				}
				cl.EncoderRaw = caddyconfig.JSONModuleObject(enc, "format", moduleName, h.warnings)

			case "level":
				if !h.NextArg() {
					return nil, h.ArgErr()
				}
				cl.Level = h.Val()
				if h.NextArg() {
					return nil, h.ArgErr()
				}

			default:
				return nil, h.Errf("unrecognized subdirective: %s", h.Val())
			}
		}

		var val namedCustomLog
		if !reflect.DeepEqual(cl, new(caddy.CustomLog)) {
			cl.Include = []string{"http.log.access"}
			val.name = fmt.Sprintf("log%d", logCounter)
			val.log = cl
			logCounter++
		}
		configValues = append(configValues, ConfigValue{
			Class: "custom_log",
			Value: val,
		})
	}
	return configValues, nil
}

// tlsCertTags maps certificate filenames to their tag.
// This is used to remember which tag is used for each
// certificate files, since we need to avoid loading
// the same certificate files more than once, overwriting
// previous tags
var tlsCertTags = make(map[string]string)

var logCounter int
