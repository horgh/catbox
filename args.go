package main

import (
	"flag"
	"fmt"
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
		return Args{}, fmt.Errorf("You must provie a configuration file.")
	}

	return Args{ConfigFile: *configFile}, nil
}
