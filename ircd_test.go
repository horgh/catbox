package main

import (
	"fmt"
	"testing"
)

func TestGetTS6ID(t *testing.T) {
	tests := []struct {
		input   uint64
		output  string
		success bool
	}{
		{0, "AAAAAA", true},
		{1, "AAAAAB", true},
		{2, "AAAAAC", true},
		{25, "AAAAAZ", true},
		{26, "AAAABA", true},
		{51, "AAAABZ", true},
		{52, "AAAACA", true},
		{308915775, "ZZZZZZ", true},
		{308915776, "", false},
	}

	for _, test := range tests {
		c := Client{ID: test.input}

		id, err := c.getTS6ID()
		if err != nil {
			if test.success {
				t.Errorf("getTS6ID(%d) = error %s, wanted %s", test.input, err,
					test.output)
				continue
			}
			continue
		}

		if !test.success {
			t.Errorf("getTS6ID(%d) = %s, wanted error", test.input, test.output)
			continue
		}

		if id != test.output {
			t.Errorf("getTS6ID(%d) = %s, wanted %s", test.input, id, test.output)
			continue
		}

		fmt.Printf("%d = %s\n", test.input, id)
	}
}
