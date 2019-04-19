package tests

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/horgh/irc"
	"github.com/stretchr/testify/require"
)

// Test that clients get TS when running MODE on a channel they are on.
//
// Also test that the TS gets propagated between servers and a client on
// another server gets the same TS
func TestMODETS(t *testing.T) {
	catbox1, err := harnessCatbox("irc1.example.org", "001")
	require.NoError(t, err, "harness catbox")
	defer catbox1.stop()

	catbox2, err := harnessCatbox("irc2.example.org", "002")
	require.NoError(t, err, "harness catbox")
	defer catbox2.stop()

	err = catbox1.linkServer(catbox2)
	require.NoError(t, err, "link catbox1 to catbox2")
	err = catbox2.linkServer(catbox1)
	require.NoError(t, err, "link catbox2 to catbox1")

	// Wait until we link.
	//
	// Retry rehashing as I observed a failing build where the second server did
	// not receive the SIGHUP, yet didn't exit. I'm not sure how that can happen
	// other than perhaps a race in signal.Notify() such that the signal handler
	// is registered so the HUP gets received but not delivered to the channel.
	linkRE := regexp.MustCompile(`Established link to irc2\.`)
	var attempts int
	for {
		if waitForLog(catbox1.LogChan, linkRE) {
			break
		}
		attempts++
		if attempts >= 5 {
			require.Fail(t, "failed to link")
		}
		require.NoError(t, err, catbox1.rehash(), "rehash catbox1")
		require.NoError(t, err, catbox2.rehash(), "rehash catbox2")
	}

	client1 := NewClient("client1", "127.0.0.1", catbox1.Port)
	recvChan1, sendChan1, _, err := client1.Start()
	require.NoError(t, err, "start client")
	defer client1.Stop()

	require.NotNil(
		t,
		waitForMessage(
			t,
			recvChan1,
			irc.Message{Command: irc.ReplyWelcome},
			"welcome from %s",
			client1.GetNick(),
		),
		"client gets welcome",
	)

	sendChan1 <- irc.Message{
		Command: "JOIN",
		Params:  []string{"#test"},
	}
	require.NotNil(
		t,
		waitForMessage(
			t,
			recvChan1,
			irc.Message{
				Command: "JOIN",
				Params:  []string{"#test"},
			},
			"%s received JOIN #test", client1.GetNick(),
		),
		"client gets JOIN message",
	)

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
	require.NotNil(t, creationTimeMessage, "client receives 329 response")

	creationTimeString := ""
	creationTime := time.Time{}
	if len(creationTimeMessage.Params) >= 3 {
		ct, err := strconv.ParseInt(creationTimeMessage.Params[2], 10, 64)
		require.NoError(t, err, "parse 329 response unixtime")
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

	require.True(
		t,
		time.Since(creationTime) <= 30*time.Second,
		"channel creation time is new enough",
	)

	// Try a client on the other server and ensure they get the same time.

	client2 := NewClient("client2", "127.0.0.1", catbox2.Port)
	recvChan2, sendChan2, _, err := client2.Start()
	require.NoError(t, err, "start client 2")
	defer client2.Stop()

	require.NotNil(
		t,
		waitForMessage(
			t,
			recvChan2,
			irc.Message{Command: irc.ReplyWelcome},
			"welcome from %s",
			client2.GetNick(),
		),
		"client 2 gets welcome",
	)

	sendChan2 <- irc.Message{
		Command: "JOIN",
		Params:  []string{"#test"},
	}
	require.NotNil(
		t,
		waitForMessage(
			t,
			recvChan2,
			irc.Message{
				Command: "JOIN",
				Params:  []string{"#test"},
			},
			"%s received JOIN #test",
			client2.GetNick(),
		),
		"client 2 gets JOIN message",
	)

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
	require.NotNil(t, creationTimeMessage, "client 2 receives 329 response")

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
