package config

import (
	"testing"
)

// TestPopulateConfig is to test the conversion of types.
func TestPopulateConfig(t *testing.T) {
	type MyType struct {
		Str string
		Abc int64
	}

	var rawValues = map[string]string{
		"str": "Hi there",
		"abc": "123",
	}

	var myT MyType
	err := PopulateStruct(&myT, rawValues)

	if err != nil {
		t.Errorf("Failed to populate: %s", err.Error())
		return
	}

	if myT.Str != "Hi there" {
		t.Errorf("Failed to parse string")
		return
	}

	if myT.Abc != 123 {
		t.Errorf("Failed to parse int")
		return
	}
}
