package irc

import "testing"

func TestSourceNick(t *testing.T) {
	tests := []struct {
		input  Message
		output string
	}{
		{Message{}, ""},
		{Message{Prefix: "blah"}, ""},
		{Message{Prefix: "!"}, ""},
		{Message{Prefix: "hi!"}, "hi"},
		{Message{Prefix: "hi!~hello@hey"}, "hi"},
	}

	for _, test := range tests {
		got := test.input.SourceNick()
		if got != test.output {
			t.Errorf("%+v.SourceNick() = %s, wanted %s", test.input, got, test.output)
		}
	}
}

func TestParseMessage(t *testing.T) {
	tests := []struct {
		input   string
		prefix  string
		command string
		params  []string
		success bool
	}{
		{":irc PRIVMSG\r\n", "irc", "PRIVMSG", []string{}, true},

		// No CRLF
		{":irc PRIVMSG", "", "", []string{}, false},

		// No CRLF
		{":irc PRIVMSG one", "", "", []string{}, false},

		// No command.
		{":irc \r\n", "", "", []string{}, false},

		{"PRIVMSG\r\n", "", "PRIVMSG", []string{}, true},

		{"PRIVMSG :hi there\r\n", "", "PRIVMSG", []string{"hi there"}, true},

		// Empty prefix.
		{": PRIVMSG \r\n", "", "", []string{}, false},

		// Stray \r.
		{"ir\rc\r\n", "", "", []string{}, false},

		{":irc PRIVMSG blah\r\n", "irc", "PRIVMSG", []string{"blah"}, true},
		{":irc 001 :Welcome\r\n", "irc", "001", []string{"Welcome"}, true},
		{":irc 001\r\n", "irc", "001", []string{}, true},

		// This is technically invalid per grammar as there is a trailing space.
		// However I permit it as we see trailing space in the wild frequently.
		{":irc PRIVMSG \r\n", "irc", "PRIVMSG", []string{}, true},

		// Invalid command.
		{":irc @01\r\n", "", "", []string{}, false},

		// No command.
		{":irc \r\n", "", "", []string{}, false},

		// Space before command.
		{":irc  PRIVMSG\r\n", "", "", []string{}, false},

		{":irc 000 hi\r\n", "irc", "000", []string{"hi"}, true},

		// It is valid to have no parameters.
		{":irc 000\r\n", "irc", "000", []string{}, true},

		// Test last param having no :
		{":irc 000 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5\r\n", "irc", "000", []string{"1",
			"2", "3", "4", "5", "6", "7", "8", "9", "0", "1", "2", "3", "4", "5"},
			true},

		// Test last param having no : nor characters. If we went by RFC 2812 then
		// this would give us an empty 15th parametr. But we go by RFC 1459 and so
		// treat it as invalid trailing space and ignore it.
		{":irc 000 1 2 3 4 5 6 7 8 9 0 1 2 3 4 \r\n", "irc", "000",
			[]string{"1", "2",
				"3", "4", "5", "6", "7", "8", "9", "0", "1", "2", "3", "4"}, true},

		// Test last param having : but no characters
		{":irc 000 1 2 3 4 5 6 7 8 9 0 1 2 3 4 :\r\n", "irc", "000",
			[]string{"1", "2",
				"3", "4", "5", "6", "7", "8", "9", "0", "1", "2", "3", "4", ""}, true},

		{":irc 000 1 2 3 4 5 6 7 8 9 0 1 2 3 4 :hi there\r\n", "irc", "000",
			[]string{"1", "2",
				"3", "4", "5", "6", "7", "8", "9", "0", "1", "2", "3", "4", "hi there"},
			true},

		// If we went by RFC 2812 then we get "hi there" as the 15th parameter
		// since : is optional by that RFC. But we favour RFC 1459 and so see too
		// many parameters.
		{":irc 000 1 2 3 4 5 6 7 8 9 0 1 2 3 4 hi there\r\n", "", "",
			[]string{}, false},

		// Malformed because \r should not appear there.
		{":irc 000 \r\r\n", "", "", []string{}, false},

		// Param must not be blank unless last param.
		// While this violates the grammar, I permit it now anyway.
		{":irc 000 \r\n", "irc", "000", []string{}, true},

		{":irc 000 0a 1b\r\n", "irc", "000", []string{"0a", "1b"}, true},

		// If we have a space then there must be a parameter (unless it's the
		// 15th).
		// While this violates the grammar, I permit it now anyway.
		{":irc 000 0 1 \r\n", "irc", "000", []string{"0", "1"}, true},

		// NUL byte is invalid.
		{":irc 000 a\x00 1 \r\n", "", "", []string{}, false},

		// : inside a middle. Valid.
		{":irc 000 a:bc\r\n", "irc", "000", []string{"a:bc"}, true},

		{":irc 000 hi :there yes\r\n", "irc", "000", []string{"hi", "there yes"},
			true},

		// : inside a middle parameter. This is valid.
		{":irc 000 hi:hi :no no\r\n", "irc", "000", []string{"hi:hi", "no no"},
			true},

		{":irc 000 hi:hi :no no :yes yes\r\n", "irc", "000", []string{"hi:hi", "no no :yes yes"},
			true},

		{":irc 000 hi:hi :no no :yes yes\n", "irc", "000", []string{"hi:hi", "no no :yes yes"},
			true},

		// Trailing whitespace is not valid here. Ratbox currently does send
		// messages like this however.
		{":irc MODE #test +o user  \r\n", "irc", "MODE",
			[]string{"#test", "+o", "user"}, true},

		// Blank topic parameter is used to unset the topic.
		{":nick!user@host TOPIC #test :\r\n", "nick!user@host", "TOPIC", []string{"#test", ""},
			true},

		{":nick!user@host MODE #test +o :blah\r\n", "nick!user@host", "MODE",
			[]string{"#test", "+o", "blah"}, true},

		{":nick!user@host MODE #test +o blah1 :blah\r\n", "nick!user@host", "MODE",
			[]string{"#test", "+o", "blah1", "blah"}, true},

		{":nick!user@host MODE #test +o :blah1 blah\r\n", "nick!user@host", "MODE",
			[]string{"#test", "+o", "blah1 blah"}, true},

		{":nick!user@host PRIVMSG #test \r\n", "nick!user@host", "PRIVMSG",
			[]string{"#test"}, true},

		{":nick!user@host PRIVMSG #test :\r\n", "nick!user@host", "PRIVMSG",
			[]string{"#test", ""}, true},

		// Message is too long. Truncate it.
		{
			":nick PRIVMSG0 #test aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\r\n",
			"nick",
			"PRIVMSG0",
			[]string{"#test", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			false,
		},

		// Message is too long. Truncate. This time we truncate in such a way that
		// we risk leaving a trailing space that separated parameters.
		{
			// bb is where \r\n should be. Entire string is 514 bytes.
			":nick PRIVMSG1 #test aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bb\r\n",
			"nick",
			"PRIVMSG1",
			[]string{"#test", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			false,
		},

		// Message is too long. Truncate. This time we truncate in such a way that
		// we leave only : that was prefixing a parameter.
		{
			// b is where \r should be. Entire string is 513 bytes.
			":nick PRIVMSG2 #test aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa :b\r\n",
			"nick",
			"PRIVMSG2",
			[]string{"#test", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ""},
			false,
		},

		// Trailing whitespace is not valid. However we accept it as we see it in
		// the wild. This tests having lots of it.
		{":irc MODE #test +o user          \r\n", "irc", "MODE",
			[]string{"#test", "+o", "user"}, true},

		// Test that inline : isn't a problem.
		{":irc MODE #test +o u:ser\r\n", "irc", "MODE",
			[]string{"#test", "+o", "u:ser"}, true},
	}

	for _, test := range tests {
		msg, err := ParseMessage(test.input)
		if err != nil {
			if test.success {
				t.Errorf("ParseMessage(%q) = %s", test.input, err)
				continue
			}

			if err == ErrTruncated {
				if msg.Prefix != test.prefix {
					t.Errorf("truncated. ParseMessage(%q) got prefix %v, wanted %v",
						test.input, msg.Prefix, test.prefix)
					continue
				}

				if msg.Command != test.command {
					t.Errorf("truncated. ParseMessage(%q) got command %v, wanted %v",
						test.input, msg.Command, test.command)
					continue
				}

				if !paramsEqual(msg.Params, test.params) {
					t.Errorf("truncated. ParseMessage(%q) got params %q, wanted %q",
						test.input, msg.Params, test.params)
					continue
				}
			}

			continue
		}

		if !test.success {
			t.Errorf("ParseMessage(%q) should have failed, but did not.", test.input)
			continue
		}

		if msg.Prefix != test.prefix {
			t.Errorf("ParseMessage(%q) got prefix %v, wanted %v", test.input,
				msg.Prefix, test.prefix)
			continue
		}

		if msg.Command != test.command {
			t.Errorf("ParseMessage(%q) got command %v, wanted %v", test.input,
				msg.Command, test.command)
			continue
		}

		if !paramsEqual(msg.Params, test.params) {
			t.Errorf("ParseMessage(%q) got params %q, wanted %q", test.input,
				msg.Params, test.params)
			continue
		}
	}
}

func TestFixLineEnding(t *testing.T) {
	tests := []struct {
		input   string
		output  string
		success bool
	}{
		{"hi", "", false},
		{"hi\n", "hi\r\n", true},
		{"hi\r\n", "hi\r\n", true},
		{"\n", "\r\n", true},
		{"\r\n", "\r\n", true},
	}

	for _, test := range tests {
		out, err := fixLineEnding(test.input)
		if err != nil {
			if !test.success {
				continue
			}

			t.Errorf("fixLineEnding(%s) failed %s, wanted %s", test.input, err,
				test.output)
			continue
		}

		if !test.success {
			t.Errorf("fixLineEnding(%s) succeeded, wanted failure", test.input)
			continue
		}

		if out != test.output {
			t.Errorf("fixLineEnding(%s) = %s, wanted %s", test.input, out,
				test.output)
		}
	}
}

func TestParsePrefix(t *testing.T) {
	var tests = []struct {
		input  string
		prefix string
		index  int
	}{
		{":irc.example.com PRIVMSG", "irc.example.com", 17},
		{":irc.example.com ", "irc.example.com", 17},
		{":irc PRIVMSG ", "irc", 5},
		{"irc.example.com", "", -1},
		{": PRIVMSG ", "", -1},
		{"irc\rexample.com", "", -1},
	}

	for _, test := range tests {
		prefix, index, err := parsePrefix(test.input)

		if err != nil {
			if test.index != -1 {
				t.Errorf("parsePrefix(%q) = error %s", test.input, err.Error())
			}
			continue
		}

		if test.index == -1 {
			t.Errorf("parsePrefix(%q) should have failed, but did not", test.input)
			continue
		}

		if prefix != test.prefix {
			t.Errorf("parsePrefix(%q) = %v, want %v", test.input, prefix,
				test.prefix)
			continue
		}

		if index != test.index {
			t.Errorf("parsePrefix(%q) = %v, want %v", test.input, index,
				test.index)
			continue
		}
	}
}

func TestParseCommand(t *testing.T) {
	var tests = []struct {
		input      string
		command    string
		startIndex int
		newIndex   int
	}{
		{":irc PRIVMSG blah\r\n", "PRIVMSG", 5, 12},
		{":irc 001 :Welcome\r\n", "001", 5, 8},
		{":irc 001\r\n", "001", 5, 8},
		{":irc PRIVMSG ", "PRIVMSG", 5, 12},
		{":irc @01\r\n", "", 5, -1},
		{":irc \r\n", "", 5, -1},
		{":irc  PRIVMSG\r\n", "", 5, -1},
	}

	for _, test := range tests {
		command, newIndex, err := parseCommand(test.input, test.startIndex)

		if err != nil {
			if test.newIndex != -1 {
				t.Errorf("parseCommand(%q) = error %s", test.input, err.Error())
			}
			continue
		}

		if test.newIndex == -1 {
			t.Errorf("parseCommand(%q) should have failed, but did not", test.input)
			continue
		}

		if command != test.command {
			t.Errorf("parseCommand(%q) = %v, want %v", test.input, command,
				test.command)
			continue
		}

		if newIndex != test.newIndex {
			t.Errorf("parseCommand(%q) = %v, want %v", test.input, newIndex,
				test.newIndex)
			continue
		}
	}
}

func TestParseParams(t *testing.T) {
	tests := []struct {
		input    string
		index    int
		params   []string
		newIndex int
	}{
		{":irc 000 hi\r\n", 8, []string{"hi"}, 11},

		// It is valid to have no parameters.
		{":irc 000\r\n", 8, nil, 8},

		// Test last param having no :
		{":irc 000 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5\r\n", 8, []string{"1", "2",
			"3", "4", "5", "6", "7", "8", "9", "0", "1", "2", "3", "4", "5"}, 38},

		// Test last param having no : nor characters. Going by RFC 2812 this would
		// give us an empty parameter as the : is optional there. But we favour RFC
		// 1459 and due to our ignoring trailing whitespace, though invalid, we get
		// only 14 parameters.
		{":irc 000 1 2 3 4 5 6 7 8 9 0 1 2 3 4 \r\n", 8, []string{"1", "2",
			"3", "4", "5", "6", "7", "8", "9", "0", "1", "2", "3", "4"}, 37},

		// Test last param having : but no characters
		{":irc 000 1 2 3 4 5 6 7 8 9 0 1 2 3 4 :\r\n", 8, []string{"1", "2",
			"3", "4", "5", "6", "7", "8", "9", "0", "1", "2", "3", "4", ""}, 38},

		// Malformed because \r should not appear there. However, parameter parsing
		// accepts this message (as having no parameters), and stops at the first
		// \r. Full message parsing will catch this as invalid.
		{":irc 000 \r\r\n", 8, []string{}, 9},

		// Must not be blank unless last param.
		// While this violates the grammar, I permit it because we see it in the
		// wild.
		{":irc 000 \r\n", 8, []string{}, 9},

		{":irc 000 0a 1b\r\n", 8, []string{"0a", "1b"}, 14},

		// If we have a space then there must be a parameter (unless it's the
		// 15th). While this violates the grammar, I permit it as we see it
		// frequently in the wild.
		{":irc 000 0 1 \r\n", 8, []string{"0", "1"}, 13},

		// This is a malformed message (NUL byte) but the parameter parsing won't
		// catch it because we stop at the NUL byte. Full message parsing catches
		// it.
		{":irc 000 a\x00 1 \r\n", 8, []string{"a"}, 10},

		// This parameter is valid as : is not the first character.
		{":irc 000 a:bc\r\n", 8, []string{"a:bc"}, 13},
	}

	for _, test := range tests {
		params, newIndex, err := parseParams(test.input, test.index)
		if err != nil {
			if test.newIndex != -1 {
				t.Errorf("parseParams(%q) = %v, want %v", test.input, err, test.params)
			}
			continue
		}

		if test.newIndex == -1 {
			t.Errorf("parseParams(%q) should have failed, but did not", test.input)
			continue
		}

		if !paramsEqual(params, test.params) {
			t.Errorf("parseParams(%q) = %v, wanted %v", test.input, params,
				test.params)
			continue
		}

		if newIndex != test.newIndex {
			t.Errorf("parseParams(%q) index = %v, wanted %v", test.input, newIndex,
				test.newIndex)
			continue
		}
	}
}

func paramsEqual(params1, params2 []string) bool {
	if len(params1) != len(params2) {
		return false
	}

	for i, v := range params1 {
		if params2[i] != v {
			return false
		}
	}

	return true
}

func TestEncodeMessage(t *testing.T) {
	tests := []struct {
		input   Message
		output  string
		success bool
	}{
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "nick",
				Params:  []string{"nick2", "hi there"},
			},
			":nick PRIVMSG nick2 :hi there\r\n",
			true,
		},
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "nick",
				Params:  []string{"nick2", " hi there"},
			},
			":nick PRIVMSG nick2 : hi there\r\n",
			true,
		},
		{
			Message{
				Command: "TOPIC",
				Prefix:  "nick",
				Params:  []string{"#test", "hi there"},
			},
			":nick TOPIC #test :hi there\r\n",
			true,
		},

		// We can have zero length TOPIC in TS6 protocol - for when the topic is
		// to be unset.
		{
			Message{
				Command: "TOPIC",
				Prefix:  "nick",
				Params:  []string{"#test", ""},
			},
			":nick TOPIC #test :\r\n",
			true,
		},

		{
			Message{
				Command: "TOPIC",
				Prefix:  "nick",
				Params:  []string{"#test", ":"},
			},
			":nick TOPIC #test ::\r\n",
			true,
		},

		// A message that encodes to longer than MaxLineLength (512) bytes
		// The encoded length of this message would be
		// 1+7+1 + 4+1 + 5+1 + 530 + 2 (crlf) = 552 bytes
		// Truncates to 512.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "nick",
				// Second parameter is 530 bytes
				Params: []string{"#test", "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz"},
			},
			":nick PRIVMSG #test abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuv\r\n",
			false,
		},

		// Another message that is too long to encode as is. Truncates.
		//
		// This time the final parameter is 2 bytes long and has no prefix, and
		// there is no space to include either of them. This is to test what
		// happens when we truncate in the situation where we drop the entire last
		// parameter.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "nick",
				Params:  []string{"#test", "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstz", "wx"},
			},
			// Length becomes 511 bytes. Not 512 because we do not include the space
			// that would separate the parameter which we cannot include at all.
			":nick PRIVMSG #test abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstz\r\n",
			false,
		},

		// Message is too long to encode.
		//
		// In this case the last parameter is 1 byte, and it has a prefix. Again
		// there is no space to include any of it. This is again to test behaviour
		// when we drop the whole last parameter.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "nick",
				Params:  []string{"#test", "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstz", ":"},
			},
			// Length becomes 511 bytes. Not 512 because we do not include the space
			// that would separate the parameter which we cannot include at all.
			":nick PRIVMSG #test abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstz\r\n",
			false,
		},

		// Message is too long to encode.
		//
		// In this case the last parameter is 1 byte, and it has a prefix. The
		// difference in this case is we can include just the prefix.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "nick",
				Params:  []string{"#test", "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrst", ":"},
			},
			":nick PRIVMSG #test abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrst :\r\n",
			false,
		},

		// A message that is too long to encode where only the prefix and the
		// command are alone enough to exceed the length. In this case it does
		// not make sense to truncate. Error out.
		{
			Message{
				Command: "PRIVMSG",
				// Prefix is 530 bytes
				Prefix: "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz",
				Params: []string{},
			},
			"",
			false,
		},

		// Too many parameters.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "hi",
				Params: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
					"11", "12", "13", "14", "15", "16"},
			},
			"",
			false,
		},

		// 15 parameters is ok.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "hi",
				Params: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
					"11", "12", "13", "14", "15"},
			},
			":hi PRIVMSG 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15\r\n",
			true,
		},

		// Inline : does not get escaped.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "hi",
				Params:  []string{"one:two", "hi there"},
			},
			":hi PRIVMSG one:two :hi there\r\n",
			true,
		},

		// Param starting with : does get escaped.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "hi",
				Params:  []string{":one:two"},
			},
			":hi PRIVMSG ::one:two\r\n",
			true,
		},

		// Need to escape first parameter, but it's not the last.
		{
			Message{
				Command: "PRIVMSG",
				Prefix:  "hi",
				Params:  []string{":one:two", "hi"},
			},
			"",
			false,
		},
	}

	for _, test := range tests {
		buf, err := test.input.Encode()
		if err != nil {
			if test.success {
				t.Errorf("Encode(%s) failed but should succeed: %s", test.input, err)
				continue
			}

			// When we truncate, check we received what we expected.
			if err == ErrTruncated {
				if buf != test.output {
					t.Errorf("Encode(%s) truncated to %s, wanted %s", test.input, buf,
						test.output)
					continue
				}
			}

			continue
		}

		if !test.success {
			t.Errorf("Encode(%s) succeeded but should fail", test.input)
			continue
		}

		if buf != test.output {
			t.Errorf("Encode(%s) = %s, wanted %s", test.input, buf, test.output)
			continue
		}
	}
}
