// Package config is a config file parser.
//
// A note on usage: Due to the fact that we use the reflect package, you must
// pass in the struct for which you want to parse config keys using all
// exported fields, or this config package cannot set those fields.
//
// Key names are case insensitive.
//
// For an example of using this package, see the test(s).
//
// For the types that we support parsing out of the struct, refer to the
// populateConfig() function.
package config

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
)

// ReadStringMap a config file and returns the keys and values in a map where
// keys and values are strings.
//
// The config file syntax is:
// key = value
//
// Lines may be commented if they begin with a '#' with only whitespace or no
// whitespace in front of the '#' character. Lines currently MAY NOT have
// trailing '#' to be treated as comments.
func ReadStringMap(path string) (map[string]string, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("invalid path. Path may not be blank")
	}

	fi, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := fi.Close(); err != nil {
			log.Printf("error closing %s: %s", path, err)
		}
	}()

	config := make(map[string]string)

	scanner := bufio.NewScanner(fi)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		if len(key) == 0 {
			return nil, fmt.Errorf("key length is 0")
		}

		_, exists := config[key]
		if exists {
			return nil, fmt.Errorf("config key defined twice: %s", key)
		}

		// Permit value to be blank.

		config[key] = value
	}

	err = scanner.Err()
	if err != nil {
		return nil, fmt.Errorf("error reading from file: %s", err)
	}

	return config, nil
}

// PopulateStruct takes values read from a config as a string map, and uses them
// to populate a struct. The values will be converted to the struct's types as
// necessary.
//
// To understand the use of reflect in this function, refer to the article Laws
// of Reflection, or the documentation of the reflect package.
func PopulateStruct(config interface{}, rawValues map[string]string) error {
	// Make a reflect.Value from the interface.
	v := reflect.ValueOf(config)

	// Access the value that the interface contains.
	elem := v.Elem()

	// Make a reflect.Type. This describes the Go type. We can use it to get
	// struct field names.
	elemType := elem.Type()

	// Iterate over every field of the struct.
	for i := 0; i < elem.NumField(); i++ {
		// Access the field.
		f := elem.Field(i)

		// Determine the field name.
		fieldName := elemType.Field(i).Name

		// We require this field was in the config file.
		rawValue, ok := rawValues[strings.ToLower(fieldName)]
		if !ok {
			return fmt.Errorf("field %s not found in config file", fieldName)
		}

		// Convert each value string, if necessary, to the necessary Go type.
		// We support a subset of types ('kinds' in reflect) currently.

		if f.Kind() == reflect.Int32 {
			converted, err := strconv.ParseInt(rawValue, 10, 32)
			if err != nil {
				return fmt.Errorf("unable to convert field %s value %s to int32: %s",
					fieldName, rawValue, err)
			}

			f.SetInt(converted)
			continue
		}

		if f.Kind() == reflect.Int64 {
			converted, err := strconv.ParseInt(rawValue, 10, 64)
			if err != nil {
				return fmt.Errorf("unable to convert field %s value %s to int64: %s",
					fieldName, rawValue, err)
			}

			f.SetInt(converted)
			continue
		}

		if f.Kind() == reflect.Uint64 {
			converted, err := strconv.ParseUint(rawValue, 10, 64)
			if err != nil {
				return fmt.Errorf("unable to convert field %s value %s to uint64: %s",
					fieldName, rawValue, err)
			}

			f.SetUint(converted)
			continue
		}

		if f.Kind() == reflect.String {
			f.SetString(rawValue)
			continue
		}

		return fmt.Errorf("field %s: Value: %s: Field kind not yet supported: %s",
			fieldName, rawValue, f.Kind().String())
	}

	return nil
}

// GetConfig reads a config file and populates a struct with what is read.
//
// We use the reflect package to populate the struct from the config.
//
// Currently every member of the struct must have had a value set in the
// config. That is, every config option is required.
func GetConfig(path string, config interface{}) error {
	// We don't need to parameter check path or keys. Why? Because path will get
	// checked when we read the config.

	// We do not need to check anything with the config as it is up to the caller
	// to ensure that they gave us a struct with members they want parsed out of
	// a config.

	// First read in the config. Every key will be associated with a value which
	// is a string.
	rawValues, err := ReadStringMap(path)
	if err != nil {
		return fmt.Errorf("unable to read config: %s: %s", err, path)
	}

	// Fill the struct with the values read from the config.
	err = PopulateStruct(config, rawValues)
	if err != nil {
		return fmt.Errorf("unable to populate config: %s", err)
	}

	return nil
}
