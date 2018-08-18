package internal

import (
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/horgh/irc"
)

// Test one client sending a message to another client.
func TestPRIVMSG(t *testing.T) {
	catbox, err := harnessCatbox("irc.example.org", "000")
	if err != nil {
		t.Fatalf("error harnessing catbox: %s", err)
	}
	defer catbox.stop()

	client1 := NewClient("client1", "127.0.0.1", catbox.Port)
	recvChan1, sendChan1, _, err := client1.Start()
	if err != nil {
		t.Fatalf("error starting client: %s", err)
	}
	defer client1.Stop()

	client2 := NewClient("client2", "127.0.0.1", catbox.Port)
	recvChan2, _, _, err := client2.Start()
	if err != nil {
		t.Fatalf("error starting client: %s", err)
	}
	defer client2.Stop()

	if waitForMessage(t, recvChan1, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client1.GetNick()) == nil {
		t.Fatalf("client1 did not get welcome")
	}
	if waitForMessage(t, recvChan2, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client2.GetNick()) == nil {
		t.Fatalf("client2 did not get welcome")
	}

	sendChan1 <- irc.Message{
		Command: "PRIVMSG",
		Params:  []string{client2.GetNick(), "hi there"},
	}

	if waitForMessage(
		t,
		recvChan2,
		irc.Message{
			Command: "PRIVMSG",
			Params:  []string{client2.GetNick(), "hi there"},
		},
		"%s received PRIVMSG from %s", client1.GetNick(), client2.GetNick(),
	) == nil {
		t.Fatalf("client1 did not receive message from client2")
	}
}

func waitForMessage(
	t *testing.T,
	ch <-chan irc.Message,
	want irc.Message,
	format string,
	a ...interface{},
) *irc.Message {
	for {
		select {
		case <-time.After(10 * time.Second):
			t.Logf("timeout waiting for message: %s", want)
			return nil
		case got := <-ch:
			if got.Command == want.Command {
				log.Printf("got command: %s", fmt.Sprintf(format, a...))
				return &got
			}
		}
	}
}
