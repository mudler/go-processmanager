package process

import (
	"io/ioutil"
	"os"
)

// WithKillSignal sets the given signal while attemping to stop. Defaults to "9"
func WithKillSignal(s string) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.KillSignal = s
		return nil
	}
}

func WithEnvironment(s ...string) Option {
	return func(cfg *Config) error {
		cfg.Environment = s
		return nil
	}
}

func WithTemporaryStateDir() func(cfg *Config) error {
	return func(cfg *Config) error {
		dir, err := ioutil.TempDir(os.TempDir(), "go-processmanager")
		cfg.StateDir = dir
		return err
	}
}

func WithStateDir(s string) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.StateDir = s
		return nil
	}
}

var EnableSTDIN Option = func(cfg *Config) error {
	cfg.Stdin = true
	return nil
}

func WithName(s string) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.Name = s
		return nil
	}
}

func WithArgs(s ...string) func(cfg *Config) error {
	return func(cfg *Config) error {
		cfg.Args = append(cfg.Args, s...)
		return nil
	}
}
