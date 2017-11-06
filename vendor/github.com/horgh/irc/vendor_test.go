package irc

import (
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v2"
)

// msg-split tests from irc-parser-tests
func TestIRCParserTestsMsgSplit(t *testing.T) {
	testFile := filepath.Join("irc-parser-tests", "tests", "msg-split.yaml")
	data, err := ioutil.ReadFile(testFile)
	if err != nil {
		t.Fatalf("error opening file: %s: %s", testFile, err)
	}

	type MsgSplitTests struct {
		Tests []struct {
			Input string
			Atoms struct {
				Source *string
				Verb   string
				Params []string
				Tags   map[string]interface{}
			}
		}
	}

	var tests *MsgSplitTests
	if err := yaml.Unmarshal(data, &tests); err != nil {
		t.Fatalf("error unmarshaling %s: %s", testFile, err)
	}

	for _, test := range tests.Tests {
		if test.Atoms.Tags != nil {
			// We don't support tags right now.
			continue
		}

		if test.Input ==
			":gravel.mozilla.org 432  #momo :Erroneous Nickname: Illegal characters" {
			// This is an invalid message. I'm not inclined to support it.
			continue
		}
		input := test.Input + "\r\n"
		msg, err := ParseMessage(input)
		if err != nil {
			t.Fatalf("error parsing message: %s: %s", test.Input, err)
		}

		wantCommand := strings.ToUpper(test.Atoms.Verb)
		if msg.Command != wantCommand {
			t.Errorf("%s: got command %s, wanted %s", test.Input, msg.Command,
				wantCommand)
			continue
		}

		if len(msg.Params) != len(test.Atoms.Params) {
			t.Errorf("%s: got %d params, wanted %d", test.Input, len(msg.Params),
				len(test.Atoms.Params))
			continue
		}

		for i, data := range test.Atoms.Params {
			if msg.Params[i] != data {
				t.Errorf("%s: param %d is %s, wanted %s", test.Input, i,
					msg.Params[i], data)
				continue
			}
		}

		prefix := ""
		if test.Atoms.Source != nil {
			prefix = *test.Atoms.Source
		}

		if msg.Prefix != prefix {
			t.Errorf("%s: prefix is %s, wanted %s", test.Input, msg.Prefix, prefix)
			continue
		}
	}
}

// msg-join tests from irc-parser-tests
func TestIRCParserTestsMsgJoin(t *testing.T) {
	testFile := filepath.Join("irc-parser-tests", "tests", "msg-join.yaml")
	data, err := ioutil.ReadFile(testFile)
	if err != nil {
		t.Fatalf("error opening file: %s: %s", testFile, err)
	}

	type MsgJoinTests struct {
		Tests []struct {
			Desc  string
			Atoms struct {
				Source string
				Verb   string
				Params []string
				Tags   map[string]interface{}
			}
			Matches []string
		}
	}

	var tests *MsgJoinTests
	if err := yaml.Unmarshal(data, &tests); err != nil {
		t.Fatalf("error unmarshaling: %s: %s", testFile, err)
	}

	for _, test := range tests.Tests {
		if test.Atoms.Tags != nil {
			// We don't support tags currently.
			continue
		}

		msg := Message{
			Prefix:  test.Atoms.Source,
			Command: test.Atoms.Verb,
			Params:  test.Atoms.Params,
		}

		buf, err := msg.Encode()
		if err != nil {
			t.Fatalf("failed to encode message for test %s: %s", test.Desc, err)
		}

		matched := false
		for _, match := range test.Matches {
			if buf == match+"\r\n" {
				matched = true
			}
		}

		if !matched {
			t.Errorf("no match: %s: got %s, wanted %v", test.Desc, buf,
				test.Matches)
		}
	}
}
