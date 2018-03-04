package main

import (
	"fmt"
	"testing"
	"time"
)

func TestErrorToQuitMessage(t *testing.T) {
	tests := []struct {
		Error error
		// This is a hack to build an error message that would otherwise trigger
		// golint warnings. I can't see an easy way to disable golint warnings on
		// particular lines.
		Message string
		Output  string
	}{
		{
			nil,
			"",
			"I/O error",
		},
		{
			fmt.Errorf("blah"),
			"",
			"blah",
		},
		{
			fmt.Errorf(""),
			"",
			"I/O error",
		},
		{
			nil,
			"hi :",
			"hi :",
		},
		{
			fmt.Errorf("hi : "),
			"",
			"hi : ",
		},
		{
			fmt.Errorf("read tcp ip:port->ip:port: i/o timeout"),
			"",
			"Ping timeout: 120 seconds",
		},
		{
			fmt.Errorf("read tcp ip:port->ip:port: read: connection reset by peer"),
			"",
			"Connection reset by peer",
		},
	}

	cb := &Catbox{
		Config: &Config{
			DeadTime: 120 * time.Second,
		},
	}

	for _, test := range tests {
		err := test.Error
		if test.Message != "" {
			err = fmt.Errorf("%s", test.Message)
		}
		output := cb.errorToQuitMessage(err)
		if output != test.Output {
			t.Errorf("errorToQuitMessage(%v) = %s, wanted %s", err, output,
				test.Output)
		}
	}
}
