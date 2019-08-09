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

package caddyhttp

import (
	"net/http"

	"github.com/caddyserver/caddy/v2"
)

func init() {
	caddy.RegisterModule(caddy.Module{
		Name: "http.handlers.vars",
		New:  func() interface{} { return new(VarsMiddleware) },
	})
	caddy.RegisterModule(caddy.Module{
		Name: "http.matchers.vars",
		New:  func() interface{} { return new(VarsMiddleware) },
	})
}

// VarsMiddleware is an HTTP middleware which sets variables
// in the context, mainly for use by placeholders.
type VarsMiddleware map[string]string

func (t VarsMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next Handler) error {
	vars := r.Context().Value(VarCtxKey).(map[string]interface{})
	repl := r.Context().Value(caddy.ReplacerCtxKey).(caddy.Replacer)
	for k, v := range t {
		keyExpanded := repl.ReplaceAll(k, "")
		valExpanded := repl.ReplaceAll(v, "")
		vars[keyExpanded] = valExpanded
	}
	return next.ServeHTTP(w, r)
}

// VarsMatcher is an HTTP request matcher which can match
// requests based on variables in the context.
type VarsMatcher map[string]string

// Match matches a request based on variables in the context.
func (m VarsMatcher) Match(r *http.Request) bool {
	vars := r.Context().Value(VarCtxKey).(map[string]string)
	repl := r.Context().Value(caddy.ReplacerCtxKey).(caddy.Replacer)
	for k, v := range m {
		keyExpanded := repl.ReplaceAll(k, "")
		valExpanded := repl.ReplaceAll(v, "")
		if vars[keyExpanded] != valExpanded {
			return false
		}
	}
	return true
}

// Interface guards
var (
	_ MiddlewareHandler = (*VarsMiddleware)(nil)
	_ RequestMatcher    = (*VarsMatcher)(nil)
)
