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

package forwardauth

import (
	"encoding/json"
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/headers"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/rewrite"
)

func init() {
	httpcaddyfile.RegisterDirective("forward_auth", parseCaddyfile)
}

// parseCaddyfile parses the forward_auth directive, which has the same syntax
// as the reverse_proxy directive (in fact, the reverse_proxy's directive
// Unmarshaler is invoked by this function) but the resulting proxy is specially
// configured for most™️ auth gateways that support forward auth. The typical
// config which looks something like this:
//
//     forward_auth auth-gateway:9091 {
//         uri /authenticate?redirect=https://auth.example.com
//         copy_headers Remote-User Remote-Email
//     }
//
// is equivalent to a reverse_proxy directive like this:
//
//     reverse_proxy auth-gateway:9091 {
//         method GET
//         rewrite /authenticate?redirect=https://auth.example.com
//
//         header_up X-Forwarded-Method {method}
//         header_up X-Forwarded-Uri {uri}
//
//         @good status 2xx
//         handle_response @good {
//             request_header {
//                 Remote-User {http.reverse_proxy.header.Remote-User}
//                 Remote-Email {http.reverse_proxy.header.Remote-Email}
//             }
//         }
//
//         handle_response {
//             copy_response_headers {
//                 exclude Connection Keep-Alive Te Trailers Transfer-Encoding Upgrade
//             }
//             copy_response
//         }
//     }
//
func parseCaddyfile(h httpcaddyfile.Helper) ([]httpcaddyfile.ConfigValue, error) {
	if !h.Next() {
		return nil, h.ArgErr()
	}

	// if the user specified a matcher token, use that
	// matcher in a route that wraps both of our routes;
	// either way, strip the matcher token and pass
	// the remaining tokens to the unmarshaler so that
	// we can gain the rest of the reverse_proxy syntax
	userMatcherSet, err := h.ExtractMatcherSet()
	if err != nil {
		return nil, err
	}

	// make a new dispenser from the remaining tokens so that we
	// can reset the dispenser back to this point for the
	// reverse_proxy unmarshaler to read from it as well
	dispenser := h.NewFromNextSegment()

	// create the reverse proxy handler
	rpHandler := &reverseproxy.Handler{
		// set up defaults for header_up; reverse_proxy already deals with
		// adding  the other three X-Forwarded-* headers, but for this flow,
		// we want to also send along the incoming method and URI since this
		// request will have a rewritten URI and method.
		Headers: &headers.Handler{
			Request: &headers.HeaderOps{
				Set: http.Header{
					"X-Forwarded-Method": []string{"{http.request.method}"},
					"X-Forwarded-Uri":    []string{"{http.request.uri}"},
				},
			},
		},

		// we always rewrite the method to GET, which implicitly
		// turns off sending the incoming request's body, which
		// allows later middleware handlers to consume it
		Rewrite: &rewrite.Rewrite{
			Method: "GET",
		},

		HandleResponse: []caddyhttp.ResponseHandler{},
	}

	// collect the headers to copy from the auth response
	// onto the original request, so they can get passed
	// through to a backend app
	headersToCopy := []string{}

	// read the subdirectives for configuring the forward_auth shortcut
	// NOTE: we delete the tokens as we go so that the reverse_proxy
	// unmarshal doesn't see these subdirectives which it cannot handle
	for dispenser.Next() {
		for dispenser.NextBlock(0) && dispenser.Nesting() == 1 {
			switch dispenser.Val() {
			case "uri":
				if !dispenser.NextArg() {
					return nil, dispenser.ArgErr()
				}
				rpHandler.Rewrite.URI = dispenser.Val()
				dispenser.Delete()
				dispenser.Delete()

			case "copy_headers":
				args := dispenser.RemainingArgs()
				dispenser.Delete()
				for _, headerField := range args {
					dispenser.Delete()
					headersToCopy = append(headersToCopy, headerField)
				}
				if len(headersToCopy) == 0 {
					return nil, dispenser.ArgErr()
				}
			}
		}
	}

	// reset the dispenser after we're done so that the reverse_proxy
	// unmarshaler can read it from the start
	dispenser.Reset()

	// the auth target URI must not be empty
	if rpHandler.Rewrite.URI == "" {
		return nil, dispenser.Errf("the 'uri' subdirective is required")
	}

	// set up handler for good responses; when a response
	// has 2xx status, then we will copy some headers from
	// the response onto the original request, and allow
	// handling to continue down the middleware chain,
	// by _not_ executing a terminal handler.
	goodResponseHandler := caddyhttp.ResponseHandler{
		Match: &caddyhttp.ResponseMatcher{
			StatusCode: []int{2},
		},
		Routes: []caddyhttp.Route{},
	}
	if len(headersToCopy) > 0 {
		handler := &headers.Handler{
			Request: &headers.HeaderOps{
				Set: http.Header{},
			},
		}

		for _, headerField := range headersToCopy {
			handler.Request.Set[headerField] = []string{
				"{http.reverse_proxy.header." + headerField + "}",
			}
		}

		goodResponseHandler.Routes = append(
			goodResponseHandler.Routes,
			caddyhttp.Route{
				HandlersRaw: []json.RawMessage{caddyconfig.JSONModuleObject(
					handler,
					"handler",
					"headers",
					nil,
				)},
			},
		)
	}
	rpHandler.HandleResponse = append(rpHandler.HandleResponse, goodResponseHandler)

	// set up handler for denial responses; when a response
	// has any other status than 2xx, then we copy the response
	// back to the client, and terminate handling.
	denialResponseHandler := caddyhttp.ResponseHandler{
		Routes: []caddyhttp.Route{
			{
				HandlersRaw: []json.RawMessage{caddyconfig.JSONModuleObject(
					&reverseproxy.CopyResponseHeadersHandler{
						Exclude: []string{
							"Connection",
							"Keep-Alive",
							"Te",
							"Trailers",
							"Transfer-Encoding",
							"Upgrade",
						},
					},
					"handler",
					"copy_response_headers",
					nil,
				)},
			},
			{
				HandlersRaw: []json.RawMessage{caddyconfig.JSONModuleObject(
					&reverseproxy.CopyResponseHandler{},
					"handler",
					"copy_response",
					nil,
				)},
			},
		},
	}
	rpHandler.HandleResponse = append(rpHandler.HandleResponse, denialResponseHandler)

	// the rest of the config is specified by the user
	// using the reverse_proxy directive syntax
	err = rpHandler.UnmarshalCaddyfile(dispenser)
	if err != nil {
		return nil, err
	}
	err = rpHandler.FinalizeUnmarshalCaddyfile(h)
	if err != nil {
		return nil, err
	}

	// create the final reverse proxy route
	rpRoute := caddyhttp.Route{
		HandlersRaw: []json.RawMessage{caddyconfig.JSONModuleObject(
			rpHandler,
			"handler",
			"reverse_proxy",
			nil,
		)},
	}

	// apply the user's matcher if any
	if userMatcherSet != nil {
		rpRoute.MatcherSetsRaw = []caddy.ModuleMap{userMatcherSet}
	}

	return []httpcaddyfile.ConfigValue{
		{
			Class: "route",
			Value: rpRoute,
		},
	}, nil
}
