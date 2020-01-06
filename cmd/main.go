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

package caddycmd

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"go.uber.org/zap"
)

// Main implements the main function of the caddy command.
// Call this if Caddy is to be the main() if your program.
func Main() {
	caddy.TrapSignals()

	switch len(os.Args) {
	case 0:
		fmt.Printf("[FATAL] no arguments provided by OS; args[0] must be command\n")
		os.Exit(caddy.ExitCodeFailedStartup)
	case 1:
		os.Args = append(os.Args, "help")
	}

	subcommandName := os.Args[1]
	subcommand, ok := commands[subcommandName]
	if !ok {
		if strings.HasPrefix(os.Args[1], "-") {
			// user probably forgot to type the subcommand
			fmt.Println("[ERROR] first argument must be a subcommand; see 'caddy help'")
		} else {
			fmt.Printf("[ERROR] '%s' is not a recognized subcommand; see 'caddy help'\n", os.Args[1])
		}
		os.Exit(caddy.ExitCodeFailedStartup)
	}

	fs := subcommand.Flags
	if fs == nil {
		fs = flag.NewFlagSet(subcommand.Name, flag.ExitOnError)
	}

	err := fs.Parse(os.Args[2:])
	if err != nil {
		fmt.Println(err)
		os.Exit(caddy.ExitCodeFailedStartup)
	}

	exitCode, err := subcommand.Func(Flags{fs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", subcommand.Name, err)
	}

	os.Exit(exitCode)
}

// handlePingbackConn reads from conn and ensures it matches
// the bytes in expect, or returns an error if it doesn't.
func handlePingbackConn(conn net.Conn, expect []byte) error {
	defer conn.Close()
	confirmationBytes, err := ioutil.ReadAll(io.LimitReader(conn, 32))
	if err != nil {
		return err
	}
	if !bytes.Equal(confirmationBytes, expect) {
		return fmt.Errorf("wrong confirmation: %x", confirmationBytes)
	}
	return nil
}

// loadConfig loads the config from configFile and adapts it
// using adapterName. If adapterName is specified, configFile
// must be also. It prints any warnings to stderr, and returns
// the resulting JSON config bytes.
func loadConfig(configFile, adapterName string) ([]byte, error) {
	// specifying an adapter without a config file is ambiguous
	if configFile == "" && adapterName != "" {
		return nil, fmt.Errorf("cannot adapt config without config file (use --config)")
	}

	// load initial config and adapter
	var config []byte
	var cfgAdapter caddyconfig.Adapter
	var err error
	if configFile != "" {
		config, err = ioutil.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %v", err)
		}
		caddy.Log().Info("using provided configuration",
			zap.String("config_file", configFile),
			zap.String("config_adapter", adapterName))
	} else if adapterName == "" {
		// as a special case when no config file or adapter
		// is specified, see if the Caddyfile adapter is
		// plugged in, and if so, try using a default Caddyfile
		cfgAdapter = caddyconfig.GetAdapter("caddyfile")
		if cfgAdapter != nil {
			config, err = ioutil.ReadFile("Caddyfile")
			if os.IsNotExist(err) {
				// okay, no default Caddyfile; pretend like this never happened
				cfgAdapter = nil
			} else if err != nil {
				// default Caddyfile exists, but error reading it
				return nil, fmt.Errorf("reading default Caddyfile: %v", err)
			} else {
				// success reading default Caddyfile
				configFile = "Caddyfile"
				caddy.Log().Info("using adjacent Caddyfile")
			}
		}
	}

	// as a special case, if a config file called "Caddyfile" was
	// specified, and no adapter is specified, assume caddyfile adapter
	// for convenience
	if filepath.Base(configFile) == "Caddyfile" && adapterName == "" {
		adapterName = "caddyfile"
	}

	// load config adapter
	if adapterName != "" {
		cfgAdapter = caddyconfig.GetAdapter(adapterName)
		if cfgAdapter == nil {
			return nil, fmt.Errorf("unrecognized config adapter: %s", adapterName)
		}
	}

	// adapt config
	if cfgAdapter != nil {
		adaptedConfig, warnings, err := cfgAdapter.Adapt(config, map[string]interface{}{
			"filename": configFile,
		})
		if err != nil {
			return nil, fmt.Errorf("adapting config using %s: %v", adapterName, err)
		}
		for _, warn := range warnings {
			msg := warn.Message
			if warn.Directive != "" {
				msg = fmt.Sprintf("%s: %s", warn.Directive, warn.Message)
			}
			fmt.Printf("[WARNING][%s] %s:%d: %s\n", adapterName, warn.File, warn.Line, msg)
		}
		config = adaptedConfig
	}

	return config, nil
}

// Flags wraps a FlagSet so that typed values
// from flags can be easily retrieved.
type Flags struct {
	*flag.FlagSet
}

// String returns the string representation of the
// flag given by name. It panics if the flag is not
// in the flag set.
func (f Flags) String(name string) string {
	return f.FlagSet.Lookup(name).Value.String()
}

// Bool returns the boolean representation of the
// flag given by name. It returns false if the flag
// is not a boolean type. It panics if the flag is
// not in the flag set.
func (f Flags) Bool(name string) bool {
	val, _ := strconv.ParseBool(f.String(name))
	return val
}

// Int returns the integer representation of the
// flag given by name. It returns 0 if the flag
// is not an integer type. It panics if the flag is
// not in the flag set.
func (f Flags) Int(name string) int {
	val, _ := strconv.ParseInt(f.String(name), 0, strconv.IntSize)
	return int(val)
}

// Float64 returns the float64 representation of the
// flag given by name. It returns false if the flag
// is not a float63 type. It panics if the flag is
// not in the flag set.
func (f Flags) Float64(name string) float64 {
	val, _ := strconv.ParseFloat(f.String(name), 64)
	return val
}

// Duration returns the duration representation of the
// flag given by name. It returns false if the flag
// is not a duration type. It panics if the flag is
// not in the flag set.
func (f Flags) Duration(name string) time.Duration {
	val, _ := time.ParseDuration(f.String(name))
	return val
}

// flagHelp returns the help text for fs.
func flagHelp(fs *flag.FlagSet) string {
	if fs == nil {
		return ""
	}

	// temporarily redirect output
	out := fs.Output()
	defer fs.SetOutput(out)

	buf := new(bytes.Buffer)
	fs.SetOutput(buf)
	fs.PrintDefaults()
	return buf.String()
}

func printEnvironment() {
	fmt.Printf("caddy.HomeDir=%s\n", caddy.HomeDir())
	fmt.Printf("caddy.AppDataDir=%s\n", caddy.AppDataDir())
	fmt.Printf("caddy.AppConfigDir=%s\n", caddy.AppConfigDir())
	fmt.Printf("caddy.ConfigAutosavePath=%s\n", caddy.ConfigAutosavePath)
	fmt.Printf("runtime.GOOS=%s\n", runtime.GOOS)
	fmt.Printf("runtime.GOARCH=%s\n", runtime.GOARCH)
	fmt.Printf("runtime.Compiler=%s\n", runtime.Compiler)
	fmt.Printf("runtime.NumCPU=%d\n", runtime.NumCPU())
	fmt.Printf("runtime.GOMAXPROCS=%d\n", runtime.GOMAXPROCS(0))
	fmt.Printf("runtime.Version=%s\n", runtime.Version())
	cwd, err := os.Getwd()
	if err != nil {
		cwd = fmt.Sprintf("<error: %v>", err)
	}
	fmt.Printf("os.Getwd=%s\n\n", cwd)
	for _, v := range os.Environ() {
		fmt.Println(v)
	}
}

// moveStorage moves the old default dataDir to the new default dataDir.
// TODO: This is TEMPORARY until the release candidates.
func moveStorage() {
	// get the home directory (the old way)
	oldHome := os.Getenv("HOME")
	if oldHome == "" && runtime.GOOS == "windows" {
		drive := os.Getenv("HOMEDRIVE")
		path := os.Getenv("HOMEPATH")
		oldHome = drive + path
		if drive == "" || path == "" {
			oldHome = os.Getenv("USERPROFILE")
		}
	}
	if oldHome == "" {
		oldHome = "."
	}
	oldDataDir := filepath.Join(oldHome, ".local", "share", "caddy")

	// nothing to do if old data dir doesn't exist
	_, err := os.Stat(oldDataDir)
	if os.IsNotExist(err) {
		return
	}

	// nothing to do if the new data dir is the same as the old one
	newDataDir := caddy.AppDataDir()
	if oldDataDir == newDataDir {
		return
	}

	logger := caddy.Log().Named("automigrate").With(
		zap.String("old_dir", oldDataDir),
		zap.String("new_dir", newDataDir))

	logger.Info("beginning one-time data directory migration",
		zap.String("details", "https://github.com/caddyserver/caddy/issues/2955"))

	// if new data directory exists, avoid auto-migration as a conservative safety measure
	_, err = os.Stat(newDataDir)
	if !os.IsNotExist(err) {
		logger.Error("new data directory already exists; skipping auto-migration as conservative safety measure",
			zap.Error(err),
			zap.String("instructions", "https://github.com/caddyserver/caddy/issues/2955#issuecomment-570000333"))
		return
	}

	// construct the new data directory's parent folder
	err = os.MkdirAll(filepath.Dir(newDataDir), 0700)
	if err != nil {
		logger.Error("unable to make new datadirectory - follow link for instructions",
			zap.String("instructions", "https://github.com/caddyserver/caddy/issues/2955#issuecomment-570000333"),
			zap.Error(err))
		return
	}

	// folder structure is same, so just try to rename (move) it;
	// this fails if the new path is on a separate device
	err = os.Rename(oldDataDir, newDataDir)
	if err != nil {
		logger.Error("new data directory already exists; skipping auto-migration as conservative safety measure - follow link for instructions",
			zap.String("instructions", "https://github.com/caddyserver/caddy/issues/2955#issuecomment-570000333"),
			zap.Error(err))
	}

	logger.Info("successfully completed one-time migration of data directory",
		zap.String("details", "https://github.com/caddyserver/caddy/issues/2955"))
}
