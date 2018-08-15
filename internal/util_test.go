package internal

import (
	"testing"

	"github.com/horgh/irc"
)

func messageIsEqual(t *testing.T, got, wanted *irc.Message) {
	if got == nil {
		t.Fatalf("received nil message")
	}

	if got.Prefix != wanted.Prefix {
		t.Fatalf("message prefix = %s, wanted %s", got.Prefix, wanted.Prefix)
	}

	if got.Command != wanted.Command {
		t.Fatalf("message command = %s, wanted %s", got.Command, wanted.Command)
	}

	if len(got.Params) != len(wanted.Params) {
		t.Fatalf("message number of params = %d, wanted %d", len(got.Params),
			len(wanted.Params))
	}

	for i := range wanted.Params {
		if got.Params[i] != wanted.Params[i] {
			t.Fatalf("param %d = %s, wanted %s", i, got.Params[i], wanted.Params[i])
		}
	}
}
