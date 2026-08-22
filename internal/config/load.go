package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const mainFile = "mqtt2prometheus.yaml"

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type MissingEnvError struct {
	Names []string
	File  string
}

func (e *MissingEnvError) Error() string {
	return fmt.Sprintf("%s: unset environment variables: %s", e.File, strings.Join(e.Names, ", "))
}

func Load(dir string) (*Config, error) {
	cfg := &Config{}
	if err := decodeFile(filepath.Join(dir, mainFile), cfg); err != nil {
		return nil, err
	}

	if cfg.Sources == "" {
		return nil, fmt.Errorf("%s: sources is required", mainFile)
	}

	paths, err := filepath.Glob(filepath.Join(dir, cfg.Sources))
	if err != nil {
		return nil, fmt.Errorf("sources %q: %w", cfg.Sources, err)
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("sources %q matched no files", cfg.Sources)
	}

	sort.Strings(paths)

	for _, path := range paths {
		src := Source{}
		if err := decodeFile(path, &src); err != nil {
			return nil, err
		}

		src.Path = path
		cfg.SourceList = append(cfg.SourceList, src)
	}

	return cfg, nil
}

func decodeFile(path string, into any) error {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	expanded, err := expandEnv(raw, path)
	if err != nil {
		return err
	}

	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)

	if err := dec.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s: file is empty", path)
		}

		return fmt.Errorf("%s: %w", path, err)
	}

	return nil
}

func expandEnv(raw []byte, path string) ([]byte, error) {
	var missing []string

	out := envRef.ReplaceAllFunc(raw, func(ref []byte) []byte {
		name := string(envRef.FindSubmatch(ref)[1])

		value, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)

			return ref
		}

		return []byte(value)
	})

	if len(missing) > 0 {
		return nil, &MissingEnvError{Names: dedupe(missing), File: filepath.Base(path)}
	}

	return out, nil
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}

		seen[s] = struct{}{}
		out = append(out, s)
	}

	return out
}
