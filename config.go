package process

import "os"

type Option func(cfg *Config) error

type Config struct {
	Name        string
	Args        []string
	Combined    bool
	StateDir    string
	KillSignal  string
	Environment []string
	Stdin       *os.File
}

func DefaultConfig() *Config {
	return &Config{
		Environment: os.Environ(),
	}
}

// Apply applies the given options to the config, returning the first error
// encountered (if any).
func (cfg *Config) Apply(opts ...Option) error {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(cfg); err != nil {
			return err
		}
	}
	return nil
}
