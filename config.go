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
	ListenHost      string
	ListenPort      string
	ListenPortTLS   string
	CertificateFile string
	KeyFile         string
	ServerName      string
	ServerInfo      string
	Version         string
	CreatedDate     string
	MOTD            string

	MaxNickLength int

	// Period of time to wait before waking server up (maximum).
	WakeupTime time.Duration

	// Period of time a client can be idle before we send it a PING.
	PingTime time.Duration

	// Period of time a client can be idle before we consider it dead.
	DeadTime time.Duration

	// Time to wait between attempts connecting to servers (minimum).
	ConnectAttemptTime time.Duration

	// Oper name to password.
	Opers map[string]string

	// Server name to its link information.
	Servers map[string]*ServerDefinition

	// TS6 SID. Must be unique in the network. Format: [0-9][A-Z0-9]{2}
	TS6SID TS6SID
}

// ServerDefinition defines how to link to a server.
type ServerDefinition struct {
	Name               string
	Hostname           string
	Port               int
	Pass               string
	TLS                bool
	LastConnectAttempt time.Time
}

// checkAndParseConfig checks configuration keys are present and in an
// acceptable format.
//
// We parse some values into alternate representations.
//
// This function populates both the server.Config and server.Opers fields.
func (cb *Catbox) checkAndParseConfig(file string) error {
	configMap, err := config.ReadStringMap(file)
	if err != nil {
		return err
	}

	requiredKeys := []string{
		"listen-host",
		"listen-port",
		"listen-port-tls",
		"certificate-file",
		"key-file",
		"server-name",
		"server-info",
		"version",
		"created-date",
		"motd",
		"max-nick-length",
		"wakeup-time",
		"ping-time",
		"dead-time",
		"connect-attempt-time",
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

	cb.Config.ListenHost = configMap["listen-host"]
	cb.Config.ListenPort = configMap["listen-port"]
	cb.Config.ListenPortTLS = configMap["listen-port-tls"]
	cb.Config.CertificateFile = configMap["certificate-file"]
	cb.Config.KeyFile = configMap["key-file"]
	cb.Config.ServerName = configMap["server-name"]
	cb.Config.ServerInfo = configMap["server-info"]
	cb.Config.Version = configMap["version"]
	cb.Config.CreatedDate = configMap["created-date"]
	cb.Config.MOTD = configMap["motd"]

	nickLen64, err := strconv.ParseInt(configMap["max-nick-length"], 10, 8)
	if err != nil {
		return fmt.Errorf("Max nick length is not valid: %s", err)
	}
	cb.Config.MaxNickLength = int(nickLen64)

	cb.Config.WakeupTime, err = time.ParseDuration(configMap["wakeup-time"])
	if err != nil {
		return fmt.Errorf("Wakeup time is in invalid format: %s", err)
	}

	cb.Config.PingTime, err = time.ParseDuration(configMap["ping-time"])
	if err != nil {
		return fmt.Errorf("Ping time is in invalid format: %s", err)
	}

	cb.Config.DeadTime, err = time.ParseDuration(configMap["dead-time"])
	if err != nil {
		return fmt.Errorf("Dead time is in invalid format: %s", err)
	}

	cb.Config.ConnectAttemptTime, err = time.ParseDuration(configMap["connect-attempt-time"])
	if err != nil {
		return fmt.Errorf("Connect attempt time is in invalid format: %s", err)
	}

	opers, err := config.ReadStringMap(configMap["opers-config"])
	if err != nil {
		return fmt.Errorf("Unable to load opers config: %s", err)
	}
	cb.Config.Opers = opers

	cb.Config.Servers = make(map[string]*ServerDefinition)
	servers, err := config.ReadStringMap(configMap["servers-config"])
	if err != nil {
		return fmt.Errorf("Unable to load servers config: %s", err)
	}

	for name, v := range servers {
		link, err := parseLink(name, v)
		if err != nil {
			return fmt.Errorf("Malformed server link information: %s: %s", name, err)
		}
		cb.Config.Servers[name] = link
	}

	if !isValidSID(configMap["ts6-sid"]) {
		return fmt.Errorf("Invalid TS6 SID")
	}
	cb.Config.TS6SID = TS6SID(configMap["ts6-sid"])

	return nil
}

// Parse the value side of a server definition from the servers config.
// Format:
// <hostname>,<port>,<password>,<tls: 1 or 0>
func parseLink(name, s string) (*ServerDefinition, error) {
	pieces := strings.Split(s, ",")
	if len(pieces) != 4 {
		return nil, fmt.Errorf("Unexpected number of fields")
	}

	hostname := strings.TrimSpace(pieces[0])
	if len(hostname) == 0 {
		return nil, fmt.Errorf("You must specify a hostname")
	}
	// We could format check hostname. But when we try to listen we'll fail.

	port, err := strconv.ParseInt(strings.TrimSpace(pieces[1]), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("Invalid port: %s: %s", pieces[1], err)
	}

	pass := strings.TrimSpace(pieces[2])
	if len(pass) == 0 {
		return nil, fmt.Errorf("You must specify a password")
	}

	return &ServerDefinition{
		Name:     name,
		Hostname: hostname,
		Port:     int(port),
		Pass:     pass,
		TLS:      pieces[3] == "1",
	}, nil
}
