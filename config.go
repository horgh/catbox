package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"summercat.com/config"
)

// Config holds a server's configuration.
type Config struct {
	ListenHost  string
	ListenPort  string
	ServerName  string
	ServerInfo  string
	Version     string
	CreatedDate string
	MOTD        string

	MaxNickLength int

	// Period of time to wait before waking server up (maximum).
	WakeupTime time.Duration

	// Period of time a client can be idle before we send it a PING.
	PingTime time.Duration

	// Period of time a client can be idle before we consider it dead.
	DeadTime time.Duration

	// Oper name to password.
	Opers map[string]string

	// Server name to its link information.
	Servers map[string]ServerDefinition

	// TS6 SID. Must be unique in the network. Format: [0-9][A-Z0-9]{2}
	TS6SID string
}

// ServerDefinition defines how to link to a server.
type ServerDefinition struct {
	Name     string
	Hostname string
	Port     int
	Pass     string
}

// checkAndParseConfig checks configuration keys are present and in an
// acceptable format.
//
// We parse some values into alternate representations.
//
// This function populates both the server.Config and server.Opers fields.
func (s *Server) checkAndParseConfig(file string) error {
	configMap, err := config.ReadStringMap(file)
	if err != nil {
		return err
	}

	requiredKeys := []string{
		"listen-host",
		"listen-port",
		"server-name",
		"server-info",
		"version",
		"created-date",
		"motd",
		"max-nick-length",
		"wakeup-time",
		"ping-time",
		"dead-time",
		"opers-config",
		"servers-config",
		"ts6-sid",
	}

	// Check each key we want is present and non-blank.
	for _, key := range requiredKeys {
		v, exists := configMap[key]
		if !exists {
			return fmt.Errorf("Missing required key: %s", key)
		}

		if len(v) == 0 {
			return fmt.Errorf("Configuration value is blank: %s", key)
		}
	}

	// Populate our struct.

	s.Config.ListenHost = configMap["listen-host"]
	s.Config.ListenPort = configMap["listen-port"]
	s.Config.ServerName = configMap["server-name"]
	s.Config.ServerInfo = configMap["server-info"]
	s.Config.Version = configMap["version"]
	s.Config.CreatedDate = configMap["created-date"]
	s.Config.MOTD = configMap["motd"]

	nickLen64, err := strconv.ParseInt(configMap["max-nick-length"], 10, 8)
	if err != nil {
		return fmt.Errorf("Max nick length is not valid: %s", err)
	}
	s.Config.MaxNickLength = int(nickLen64)

	s.Config.WakeupTime, err = time.ParseDuration(configMap["wakeup-time"])
	if err != nil {
		return fmt.Errorf("Wakeup time is in invalid format: %s", err)
	}

	s.Config.PingTime, err = time.ParseDuration(configMap["ping-time"])
	if err != nil {
		return fmt.Errorf("Ping time is in invalid format: %s", err)
	}

	s.Config.DeadTime, err = time.ParseDuration(configMap["dead-time"])
	if err != nil {
		return fmt.Errorf("Dead time is in invalid format: %s", err)
	}

	opers, err := config.ReadStringMap(configMap["opers-config"])
	if err != nil {
		return fmt.Errorf("Unable to load opers config: %s", err)
	}
	s.Config.Opers = opers

	s.Config.Servers = make(map[string]ServerDefinition)
	servers, err := config.ReadStringMap(configMap["servers-config"])
	if err != nil {
		return fmt.Errorf("Unable to load servers config: %s", err)
	}

	for name, v := range servers {
		link, err := parseLink(name, v)
		if err != nil {
			return fmt.Errorf("Malformed server link information: %s: %s", name, err)
		}
		s.Config.Servers[name] = link
	}

	if !isValidSID(configMap["ts6-sid"]) {
		return fmt.Errorf("Invalid TS6 SID")
	}
	s.Config.TS6SID = configMap["ts6-sid"]

	return nil
}

// Parse the value side of a server definition from the servers config.
// Format:
// <hostname>,<port>,<password>
func parseLink(name, s string) (ServerDefinition, error) {
	pieces := strings.Split(s, ",")
	if len(pieces) != 3 {
		return ServerDefinition{}, fmt.Errorf("Unexpected number of fields")
	}

	hostname := strings.TrimSpace(pieces[0])
	if len(hostname) == 0 {
		return ServerDefinition{}, fmt.Errorf("You must specify a hostname")
	}
	// TODO: Format check hostname

	port, err := strconv.ParseInt(strings.TrimSpace(pieces[1]), 10, 32)
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("Invalid port: %s: %s", pieces[1], err)
	}

	pass := strings.TrimSpace(pieces[2])
	if len(pass) == 0 {
		return ServerDefinition{}, fmt.Errorf("You must specify a password")
	}

	return ServerDefinition{
		Name:     name,
		Hostname: hostname,
		Port:     int(port),
		Pass:     pass,
	}, nil
}
