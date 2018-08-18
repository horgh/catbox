package tests

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/horgh/irc"
)

// Test that clients get TS when running MODE on a channel they are on.
//
// Also test that the TS gets propagated between servers and a client on
// another server gets the same TS
func TestMODETS(t *testing.T) {
	catbox1, err := harnessCatbox("irc1.example.org", "001")
	if err != nil {
		t.Fatalf("error harnessing catbox: %s", err)
	}
	defer catbox1.stop()

	catbox2, err := harnessCatbox("irc2.example.org", "002")
	if err != nil {
		t.Fatalf("error harnessing catbox: %s", err)
	}
	defer catbox2.stop()

	if err := catbox1.linkServer(catbox2); err != nil {
		t.Fatalf("error linking catbox1 to catbox2: %s", err)
	}
	if err := catbox2.linkServer(catbox1); err != nil {
		t.Fatalf("error linking catbox2 to catbox1: %s", err)
	}

	linkRE := regexp.MustCompile(`Established link to irc2\.`)
	if !waitForLog(catbox1.LogChan, linkRE) {
		t.Fatalf("failed to see servers link")
	}

	client1 := NewClient("client1", "127.0.0.1", catbox1.Port)
	recvChan1, sendChan1, _, err := client1.Start()
	if err != nil {
		t.Fatalf("error starting client: %s", err)
	}
	defer client1.Stop()

	if waitForMessage(t, recvChan1, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client1.GetNick()) == nil {
		t.Fatalf("client1 did not get welcome")
	}

	sendChan1 <- irc.Message{
		Command: "JOIN",
		Params:  []string{"#test"},
	}
	if waitForMessage(
		t,
		recvChan1,
		irc.Message{
			Command: "JOIN",
			Params:  []string{"#test"},
		},
		"%s received JOIN #test", client1.GetNick(),
	) == nil {
		t.Fatalf("client1 did not receive JOIN message")
	}

	sendChan1 <- irc.Message{
		Command: "MODE",
		Params:  []string{"#test"},
	}
	creationTimeMessage := waitForMessage(
		t,
		recvChan1,
		irc.Message{
			Command: "329",
		},
		"%s received 329 response after MODE command", client1.GetNick(),
	)
	if creationTimeMessage == nil {
		t.Fatalf("client1 did not receive 329 response")
	}

	creationTimeString := ""
	creationTime := time.Time{}
	if len(creationTimeMessage.Params) >= 3 {
		ct, err := strconv.ParseInt(creationTimeMessage.Params[2], 10, 64)
		if err != nil {
			t.Fatalf("error parsing 329 response unixtime: %s", err)
		}
		creationTimeString = creationTimeMessage.Params[2]
		creationTime = time.Unix(ct, 0)
	}

	messageIsEqual(
		t,
		creationTimeMessage,
		&irc.Message{
			Prefix:  catbox1.Name,
			Command: "329",
			Params:  []string{client1.GetNick(), "#test", creationTimeString},
		},
	)

	if time.Since(creationTime) > 30*time.Second {
		t.Fatalf("channel creation time is too far in the past: %s", creationTime)
	}

	// Try a client on the other server and ensure they get the same time.

	client2 := NewClient("client2", "127.0.0.1", catbox2.Port)
	recvChan2, sendChan2, _, err := client2.Start()
	if err != nil {
		t.Fatalf("error starting client: %s", err)
	}
	defer client2.Stop()

	if waitForMessage(t, recvChan2, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client2.GetNick()) == nil {
		t.Fatalf("client2 did not get welcome")
	}

	sendChan2 <- irc.Message{
		Command: "JOIN",
		Params:  []string{"#test"},
	}
	if waitForMessage(
		t,
		recvChan2,
		irc.Message{
			Command: "JOIN",
			Params:  []string{"#test"},
		},
		"%s received JOIN #test", client2.GetNick(),
	) == nil {
		t.Fatalf("client2 did not receive JOIN message")
	}

	sendChan2 <- irc.Message{
		Command: "MODE",
		Params:  []string{"#test"},
	}
	creationTimeMessage2 := waitForMessage(
		t,
		recvChan2,
		irc.Message{
			Command: "329",
		},
		"%s received 329 response after MODE command", client2.GetNick(),
	)
	if creationTimeMessage == nil {
		t.Fatalf("client2 did not receive 329 response")
	}

	messageIsEqual(
		t,
		creationTimeMessage2,
		&irc.Message{
			Prefix:  catbox2.Name,
			Command: "329",
			Params:  []string{client2.GetNick(), "#test", creationTimeString},
		},
	)
}
