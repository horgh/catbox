package main

import (
	"fmt"
	"testing"
)

func TestCanonicalizeNick(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"ABC", "abc"},
		{"abc", "abc"},
		{"Abc", "abc"},
		{"a12", "a12"},
		{"A12", "a12"},
		{"{}|^~", "{}|^~"},
		{"[]\\~", "{}|~"},
		{"-[\\]^_`{|}", "-{|}^_`{|}"},
	}

	for _, test := range tests {
		out := canonicalizeNick(test.input)
		if out != test.output {
			t.Errorf("canonicalizeNick(%s) = %s, wanted %s", test.input, out,
				test.output)
		}
	}
}

func TestMakeTS6ID(t *testing.T) {
	tests := []struct {
		input   uint64
		output  string
		success bool
	}{
		{0, "AAAAAA", true},
		{1, "AAAAAB", true},
		{2, "AAAAAC", true},
		{25, "AAAAAZ", true},
		{26, "AAAAA0", true},
		{27, "AAAAA1", true},
		{28, "AAAAA2", true},
		{29, "AAAAA3", true},
		{30, "AAAAA4", true},
		{35, "AAAAA9", true},
		{36, "AAAABA", true},
		{72, "AAAACA", true},
		{98, "AAAAC0", true},
		{107, "AAAAC9", true},
		{1572120575, "Z99999", true},
		{1572120576, "", false},
	}

	for _, test := range tests {
		id, err := makeTS6ID(test.input)
		if err != nil {
			if test.success {
				t.Errorf("makeTS6ID(%d) = error %s, wanted %s", test.input, err,
					test.output)
				continue
			}
			continue
		}

		if !test.success {
			t.Errorf("makeTS6ID(%d) = %s, wanted error", test.input, test.output)
			continue
		}

		if id != TS6ID(test.output) {
			t.Errorf("makeTS6ID(%d) = %s, wanted %s", test.input, id, test.output)
			continue
		}

		fmt.Printf("%d = %s\n", test.input, id)
	}
}

//func TestMassGetTS6IDs(t *testing.T) {
//	ids := map[string]struct{}{}
//
//	c := UserClient{}
//	for i := uint64(0); i < 1572120576; i++ {
//		c.ID = i
//		ts6, err := c.getTS6ID()
//		if err != nil {
//			t.Errorf("i %d %s", i, err)
//			return
//		}
//
//		_, exists := ids[ts6]
//		if exists {
//			t.Errorf("i %d dupe", i)
//			return
//		}
//
//		ids[ts6] = struct{}{}
//
//		if i%1000000 == 0 {
//			fmt.Printf("%d...", i)
//		}
//	}
//}

func TestUserMatchesMask(t *testing.T) {
	tests := []struct {
		inputUser     User
		inputUserMask string
		inputHostMask string
		output        bool
	}{
		{
			inputUser:     User{Username: "test", Hostname: "127.0.0.1"},
			inputUserMask: "test",
			inputHostMask: "127.0.0.1",
			output:        true,
		},
		{
			inputUser:     User{Username: "test", Hostname: "127.0.0.1"},
			inputUserMask: "*",
			inputHostMask: "127.0.0.1",
			output:        true,
		},
		{
			inputUser:     User{Username: "test", Hostname: "127.0.0.1"},
			inputUserMask: "test",
			inputHostMask: "*",
			output:        true,
		},
		{
			inputUser:     User{Username: "test", Hostname: "127.0.0.1"},
			inputUserMask: "t?st",
			inputHostMask: "127.0.0.1",
			output:        true,
		},
		{
			inputUser:     User{Username: "test", Hostname: "127.0.0.1"},
			inputUserMask: "*est",
			inputHostMask: "127.0.0.1",
			output:        true,
		},
		{
			inputUser:     User{Username: "test", Hostname: "127.0.0.1"},
			inputUserMask: "*test",
			inputHostMask: "127.0.0.1",
			output:        true,
		},
		{
			inputUser:     User{Username: "test", Hostname: "127.0.0.1"},
			inputUserMask: "test",
			inputHostMask: "127.0.0.*",
			output:        true,
		},

		{
			inputUser:     User{Username: "test", Hostname: "127.0.0.1"},
			inputUserMask: "*tst",
			inputHostMask: "127.0.0.1",
			output:        false,
		},
	}

	for _, test := range tests {
		output := test.inputUser.matchesMask(test.inputUserMask, test.inputHostMask)
		if output != test.output {
			t.Errorf("matchesMask(%s, %s) = %v, wanted %v", test.inputUserMask,
				test.inputHostMask, output, test.output)
		}
	}
}

func TestParseAndResolveUmodeChanges(t *testing.T) {
	tests := []struct {
		inputModes         string
		inputCurrentModes  map[byte]struct{}
		outputSetModes     map[byte]struct{}
		outputUnsetModes   map[byte]struct{}
		outputUnknownModes map[byte]struct{}
		success            bool
	}{
		{
			inputCurrentModes:  map[byte]struct{}{'i': struct{}{}},
			inputModes:         "-i",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{'i': struct{}{}},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'i': struct{}{}},
			inputModes:         "i",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'o': struct{}{}},
			inputModes:         "+C-C",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'o': struct{}{}},
			inputModes:         "+C",
			outputSetModes:     map[byte]struct{}{'C': struct{}{}},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'i': struct{}{}},
			inputModes:         "+C",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'i': struct{}{}},
			inputModes:         "-C",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'i': struct{}{}},
			inputModes:         "+o",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'o': struct{}{}},
			inputModes:         "+C1",
			outputSetModes:     map[byte]struct{}{'C': struct{}{}},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{'1': struct{}{}},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'o': struct{}{}},
			inputModes:         "C1",
			outputSetModes:     map[byte]struct{}{'C': struct{}{}},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{'1': struct{}{}},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'o': struct{}{}, 'C': struct{}{}},
			inputModes:         "+C",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'o': struct{}{}, 'C': struct{}{}},
			inputModes:         "-C",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{'C': struct{}{}},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'o': struct{}{}, 'C': struct{}{}},
			inputModes:         "-o",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{'o': struct{}{}, 'C': struct{}{}},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
		{
			inputCurrentModes:  map[byte]struct{}{'o': struct{}{}, 'C': struct{}{}},
			inputModes:         "-oC",
			outputSetModes:     map[byte]struct{}{},
			outputUnsetModes:   map[byte]struct{}{'o': struct{}{}, 'C': struct{}{}},
			outputUnknownModes: map[byte]struct{}{},
			success:            true,
		},
	}

	for _, test := range tests {
		setModes, unsetModes, unknownModes, err := parseAndResolveUmodeChanges(
			test.inputModes, test.inputCurrentModes)
		if err != nil {
			if test.success {
				t.Errorf("parseAndResolveUmodeChanges(%s, %v) failed, should have succeeded",
					test.inputModes, test.inputCurrentModes)
				continue
			}
			continue
		}

		if !test.success {
			t.Errorf("parseAndResolveUmodeChanges(%s, %v) succeeded, should have failed",
				test.inputModes, test.inputCurrentModes)
			continue
		}

		if !modesAreEqual(setModes, test.outputSetModes) {
			t.Errorf("parseAndResolveUmodeChanges(%s, %v) set modes = %v, wanted %v",
				test.inputModes, test.inputCurrentModes, setModes, test.outputSetModes)
			continue
		}

		if !modesAreEqual(unsetModes, test.outputUnsetModes) {
			t.Errorf("parseAndResolveUmodeChanges(%s, %v) unset modes = %v, wanted %v",
				test.inputModes, test.inputCurrentModes, unsetModes,
				test.outputUnsetModes)
			continue
		}

		if !modesAreEqual(unknownModes, test.outputUnknownModes) {
			t.Errorf("parseAndResolveUmodeChanges(%s, %v) unknown modes = %v, wanted %v",
				test.inputModes, test.inputCurrentModes, unknownModes,
				test.outputUnknownModes)
			continue
		}
	}
}

func modesAreEqual(mode0 map[byte]struct{}, mode1 map[byte]struct{}) bool {
	for mode := range mode0 {
		_, exists := mode1[mode]
		if !exists {
			return false
		}
	}
	for mode := range mode1 {
		_, exists := mode0[mode]
		if !exists {
			return false
		}
	}
	return true
}

func TestIsValidUser(t *testing.T) {
	tests := []struct {
		Input string
		Valid bool
	}{
		{"hi", true},

		// We don't let user end with .
		{"hi.", false},

		// _ can't be in first position.
		{"_hi", false},

		{"hi_there", true},
	}

	for _, test := range tests {
		if !isValidUser(test.Input) {
			if !test.Valid {
				continue
			}

			t.Errorf("isValidUser(%s) invalid, wanted valid", test.Input)
			continue
		}
	}
}

func TestIsValidNick(t *testing.T) {
	tests := []struct {
		Input string
		Valid bool
	}{
		{"hi", true},

		// - can't be in first position.
		{"-hi", false},

		// Digits can't be in first position.
		{"0hi", false},
		{"9hi", false},

		{"hi_there", true},
		{"hi_there19", true},

		{"[HiThere]", true},
		{"hi`", true},
	}

	for _, test := range tests {
		if !isValidNick(25, test.Input) {
			if !test.Valid {
				continue
			}

			t.Errorf("isValidNick(%s) invalid, wanted valid", test.Input)
			continue
		}
	}
}

func TestIssueKillToServer(t *testing.T) {
	tests := []struct {
		Killer *User
		Killee *User
		Reason string
	}{
		// User killing user.
		{
			&User{
				DisplayNick: "killer_",
				Username:    "killer",
				Hostname:    "example.com",
				UID:         TS6UID("000AAAAAA"),
			},
			&User{
				DisplayNick: "killee_",
				Username:    "killee",
				Hostname:    "example2.com",
				UID:         TS6UID("000AAAAAB"),
			},
			"go away",
		},

		// Try server kill.
		{
			nil,
			&User{
				DisplayNick: "killee_",
				Username:    "killee",
				Hostname:    "example2.com",
				UID:         TS6UID("000AAAAAB"),
			},
			"go away",
		},

		// User kill with blank reason.
		{
			&User{
				DisplayNick: "killer_",
				Username:    "killer",
				Hostname:    "example.com",
				UID:         TS6UID("000AAAAAA"),
			},
			&User{
				DisplayNick: "killee_",
				Username:    "killee",
				Hostname:    "example2.com",
				UID:         TS6UID("000AAAAAB"),
			},
			"",
		},

		// Server kill with blank reason.
		{
			nil,
			&User{
				DisplayNick: "killee_",
				Username:    "killee",
				Hostname:    "example2.com",
				UID:         TS6UID("000AAAAAB"),
			},
			"",
		},
	}

	for _, test := range tests {
		cb := Catbox{
			Config: &Config{
				ServerName: "irc.example.com",
				TS6SID:     "000",
			},
		}

		ls := &LocalServer{
			LocalClient: &LocalClient{},
			Server:      &Server{Name: "irc.example.com"},
		}

		msgs := cb.issueKillToServer(ls, test.Killer, test.Killee, test.Reason)

		if len(msgs) != 1 {
			t.Errorf("issueKillToServer(%s, %s, %s, %s) resulted in %d messages, want %d",
				ls, test.Killer, test.Killee, test.Reason, len(msgs), 1)
			continue
		}

		msg := msgs[0]

		if msg.Target != ls.LocalClient {
			t.Errorf("issueKillToServer(%s, %s, %s, %s) had unexpected target %s, wanted %s",
				ls, test.Killer, test.Killee, test.Reason, msg.Target, ls.LocalClient)
			continue
		}

		if test.Killer == nil {
			if msg.Message.Prefix != string(cb.Config.TS6SID) {
				t.Errorf("issueKillToServer(%s, %s, %s, %s) had unexpected prefix %s, wanted %s",
					ls, test.Killer, test.Killee, test.Reason, msg.Message.Prefix,
					cb.Config.TS6SID)
				continue
			}
		} else {
			if msg.Message.Prefix != string(test.Killer.UID) {
				t.Errorf("issueKillToServer(%s, %s, %s, %s) had unexpected prefix %s, wanted %s",
					ls, test.Killer, test.Killee, test.Reason, msg.Message.Prefix,
					test.Killer.UID)
				continue
			}
		}

		if msg.Message.Command != "KILL" {
			t.Errorf("issueKillToServer(%s, %s, %s, %s) had unexpected command %s, wanted %s",
				ls, test.Killer, test.Killee, test.Reason, msg.Message.Command, "KILL")
			continue
		}

		if len(msg.Message.Params) != 2 {
			t.Errorf("issueKillToServer(%s, %s, %s, %s) had unexpected number of params, had %d wanted %d",
				ls, test.Killer, test.Killee, test.Reason, len(msg.Message.Params), 2)
			continue
		}

		if msg.Message.Params[0] != string(test.Killee.UID) {
			t.Errorf("issueKillToServer(%s, %s, %s, %s) had unexpected killee param, had %s wanted %s",
				ls, test.Killer, test.Killee, test.Reason, msg.Message.Params[0],
				test.Killee.UID)
			continue
		}

		wantedReason := ""
		if test.Killer == nil {
			wantedReason = fmt.Sprintf("%s (%s)", cb.Config.ServerName, test.Reason)
		} else {
			wantedReason = fmt.Sprintf("%s!%s!%s!%s (%s)", cb.Config.ServerName,
				test.Killer.Hostname, test.Killer.Username, test.Killer.DisplayNick,
				test.Reason)
		}

		if msg.Message.Params[1] != wantedReason {
			t.Errorf("issueKillToServer(%s, %s, %s, %s) had unexpected reason param, had %s wanted %s",
				ls, test.Killer, test.Killee, test.Reason, msg.Message.Params[1],
				wantedReason)
			continue
		}
	}
}
