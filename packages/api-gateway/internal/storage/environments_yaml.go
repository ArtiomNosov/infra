package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"gopkg.in/yaml.v3"
)

type EnvironmentsYAML struct {
	Environments []*Environment `yaml:"environments"`
}

type EnvironmentsFile struct {
	path string
}

func NewEnvironmentsFile(path string) *EnvironmentsFile {
	return &EnvironmentsFile{path: path}
}

func (f *EnvironmentsFile) Load() (*EnvironmentsYAML, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &EnvironmentsYAML{Environments: []*Environment{}}, nil
		}
		return nil, err
	}

	var config EnvironmentsYAML
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func (f *EnvironmentsFile) Save(config *EnvironmentsYAML) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmpPath := f.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	if err := os.Chmod(tmpPath, 0644); err != nil {
		os.Remove(tmpPath)
		return err
	}

	file, err := os.OpenFile(tmpPath, os.O_WRONLY, 0)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, f.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}


