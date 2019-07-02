package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// Args are command line arguments.
type Args struct {
	ConfigFile string
	ListenFD   int
	ServerName string
	SID        string
}

func getArgs() *Args {
	configFile := flag.String("conf", "", "Configuration file.")
	fd := flag.Int(
		"listen-fd",
		-1,
		"File descriptor with listening port to use (optional).",
	)
	serverName := flag.String(
		"server-name",
		"",
		"Server name. Overrides server-name from config.",
	)
	sid := flag.String(
		"sid",
		"",
		"SID. Overrides ts6-sid from config.",
	)

	flag.Parse()

	if len(*configFile) == 0 {
		printUsage(fmt.Errorf("you must provide a configuration file"))
		return nil
	}

	configPath, err := filepath.Abs(*configFile)
	if err != nil {
		printUsage(fmt.Errorf(
			"unable to determine path to the configuration file: %s", err))
		return nil
	}

	return &Args{
		ConfigFile: configPath,
		ListenFD:   *fd,
		ServerName: *serverName,
		SID:        *sid,
	}
}

func printUsage(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "%s\n", err)                           // nolint: gas
	_, _ = fmt.Fprintf(os.Stderr, "Usage: %s <arguments>\n", os.Args[0]) // nolint: gas
	flag.PrintDefaults()
}
