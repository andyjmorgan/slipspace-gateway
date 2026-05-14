package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func loadResponsesFile(path string) ([]CannedResponse, error) {
	// Path is supplied via --responses; reading user-named files is the
	// whole point of the flag, so the gosec G304 warning is intentional.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}

	ext := strings.ToLower(extOf(path))
	switch ext {
	case ".yaml", ".yml":
		return decodeYAML(data)
	case ".json":
		return decodeJSON(data)
	default:
		return nil, fmt.Errorf("unsupported responses file extension %q (want .yaml, .yml, or .json)", ext)
	}
}

func extOf(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return path[i:]
}

func decodeJSON(data []byte) ([]CannedResponse, error) {
	var out []CannedResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode json responses: %w", err)
	}
	return out, nil
}

func decodeYAML(data []byte) ([]CannedResponse, error) {
	var out []CannedResponse
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode yaml responses: %w", err)
	}
	return out, nil
}
