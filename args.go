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
}

func getArgs() *Args {
	configFile := flag.String("conf", "", "Configuration file.")

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
	}
}

func printUsage(err error) {
	fmt.Fprintf(os.Stderr, "%s\n", err)
	fmt.Fprintf(os.Stderr, "Usage: %s <arguments>\n", os.Args[0])
	flag.PrintDefaults()
}
