package splitter

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	bolt "go.etcd.io/bbolt"
)

// Prefix represents which paths to split
type Prefix struct {
	From     string
	To       string
	Excludes []string
}

// NewPrefix returns a new prefix, sanitizing the input
func NewPrefix(from, to string, excludes []string) *Prefix {
	// remove the trailing slash (to avoid duplicating cache)
	from = strings.TrimRight(from, "/")
	to = strings.TrimRight(to, "/")

	// remove trailing slashes from excludes (as it does not mean anything)
	for i, exclude := range excludes {
		excludes[i] = strings.TrimRight(exclude, "/")
	}

	return &Prefix{
		From:     from,
		To:       to,
		Excludes: excludes,
	}
}

// Config represents a split configuration
type Config struct {
	Prefixes   []*Prefix
	Path       string
	Origin     string
	Commit     string
	Target     string
	GitVersion string
	Debug      bool
	Scratch    bool

	// for advanced usage only
	// naming and types subject to change anytime!
	Logger *log.Logger
	DB     *bolt.DB
	RepoMu *sync.Mutex
	Repo   *git.Repository
	Git    int
}

var supportedGitVersions = map[string]int{
	"<1.8.2": 1,
	"<2.8.0": 2,
	"latest": 3,
}

// Split splits a configuration
func Split(config *Config, result *Result) error {
	state, err := newState(config, result)
	if err != nil {
		return err
	}
	defer state.close()
	return state.split()
}

// Validate validates the configuration
func (config *Config) Validate() error {
	err := plumbing.ReferenceName(config.Origin).Validate()
	if err != nil {
		return fmt.Errorf("the origin is not a valid Git reference: %w", err)
	}

	err = plumbing.ReferenceName(config.Target).Validate()
	if config.Target != "" && err != nil {
		return fmt.Errorf("the target is not a valid Git reference: %w", err)
	}

	git, ok := supportedGitVersions[config.GitVersion]
	if !ok {
		return fmt.Errorf(`the git version can only be one of "<1.8.2", "<2.8.0", or "latest"`)
	}
	config.Git = git

	return nil
}
