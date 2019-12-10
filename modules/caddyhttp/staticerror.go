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
	"fmt"
	"net/http"
	"strconv"

	"github.com/caddyserver/caddy/v2"
)

func init() {
	caddy.RegisterModule(StaticError{})
}

// StaticError implements a simple handler that returns an error.
type StaticError struct {
	Error      string     `json:"error,omitempty"`
	StatusCode WeakString `json:"status_code,omitempty"`
}

// CaddyModule returns the Caddy module information.
func (StaticError) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.error",
		New: func() caddy.Module { return new(StaticError) },
	}
}

func (e StaticError) ServeHTTP(w http.ResponseWriter, r *http.Request, _ Handler) error {
	repl := r.Context().Value(caddy.ReplacerCtxKey).(caddy.Replacer)

	statusCode := http.StatusInternalServerError
	if codeStr := e.StatusCode.String(); codeStr != "" {
		intVal, err := strconv.Atoi(repl.ReplaceAll(codeStr, ""))
		if err != nil {
			return Error(http.StatusInternalServerError, err)
		}
		statusCode = intVal
	}

	return Error(statusCode, fmt.Errorf("%s", e.Error))
}

// Interface guard
var _ MiddlewareHandler = (*StaticError)(nil)
