package config

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// UnmarshalStrict unmarshals YAML or JSON data into target struct with strict unknown field checking.
func UnmarshalStrict(data []byte, target interface{}) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty configuration content")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(trimmed))
	decoder.KnownFields(true) // Rejects unknown fields with line and column numbers

	if err := decoder.Decode(target); err != nil {
		if err == io.EOF {
			return fmt.Errorf("empty configuration content")
		}
		return fmt.Errorf("configuration syntax or schema error: %w", err)
	}

	// Ensure there are no unexpected trailing documents
	var extra interface{}
	err := decoder.Decode(&extra)
	if err == nil {
		return fmt.Errorf("unexpected extra data in configuration")
	}
	if err != io.EOF {
		return fmt.Errorf("unexpected extra data in configuration: %w", err)
	}

	return nil
}

// Unmarshal parses raw bytes into a PolicyConfig struct.
func Unmarshal(data []byte) (*PolicyConfig, error) {
	var cfg PolicyConfig
	if err := UnmarshalStrict(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
