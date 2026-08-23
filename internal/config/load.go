package config

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

func Load(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open cluster configuration %q: %w", path, err)
	}
	defer file.Close()

	configuration := Default()
	decoder := yaml.NewDecoder(file)
	// 未知字段通常来自拼写错误，必须直接拒绝，不能静默忽略。
	decoder.KnownFields(true)

	if err := decoder.Decode(&configuration); err != nil {
		return nil, fmt.Errorf("decode cluster configuration %q: %w", path, err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("decode cluster configuration %q: %w", path, err)
		}
		return nil, fmt.Errorf("cluster configuration %q must contain exactly one YAML document", path)
	}

	if err := configuration.Validate(); err != nil {
		return nil, fmt.Errorf("validate cluster configuration %q: %w", path, err)
	}

	return &configuration, nil
}
