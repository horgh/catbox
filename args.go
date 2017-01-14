package main

import (
	"flag"
	"fmt"
	"path/filepath"
)

// Args are command line arguments.
type Args struct {
	ConfigFile string
}

func getArgs() (Args, error) {
	configFile := flag.String("config", "", "Configuration file.")

	flag.Parse()

	if len(*configFile) == 0 {
		flag.PrintDefaults()
		return Args{}, fmt.Errorf("you must provide a configuration file")
	}

	configPath, err := filepath.Abs(*configFile)
	if err != nil {
		return Args{}, fmt.Errorf("unable to determine absolute path to config file: %s: %s",
			*configFile, err)
	}

	return Args{ConfigFile: configPath}, nil
}
