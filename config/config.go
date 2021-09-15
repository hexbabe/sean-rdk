// Package config defines the structures to configure a robot and its connected parts.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-errors/errors"

	"go.viam.com/utils"
	"go.viam.com/utils/pexec"

	"go.viam.com/core/board"
	functionvm "go.viam.com/core/function/vm"
)

// SortComponents sorts list of components topologically based off what other components they depend on.
func SortComponents(components []Component) ([]Component, error) {
	componentToConfig := make(map[string]Component, len(components))
	dependencies := map[string][]string{}

	for _, config := range components {
		if _, ok := componentToConfig[config.Name]; ok {
			return nil, errors.Errorf("component name %q is not unique", config.Name)
		}
		componentToConfig[config.Name] = config
		dependencies[config.Name] = config.DependsOn
	}

	for name, dps := range dependencies {
		for _, depName := range dps {
			if _, ok := componentToConfig[depName]; !ok {
				return nil, utils.NewConfigValidationError(fmt.Sprintf("%s.%s", "components", name), errors.Errorf("dependency %q does not exist", depName))
			}
		}
	}

	sortedCmps := make([]Component, 0, len(components))
	visited := map[string]bool{}

	var dfsHelper func(string, []string) error
	dfsHelper = func(name string, path []string) error {
		for idx, cmpName := range path {
			if name == cmpName {
				return errors.Errorf("circular dependency detected in component list between %s", strings.Join(path[idx:], ", "))
			}
		}

		path = append(path, name)
		if _, ok := visited[name]; ok {
			return nil
		}
		visited[name] = true
		dps := dependencies[name]
		for _, dp := range dps {
			// create a deep copy of current path
			pathCopy := make([]string, len(path))
			copy(pathCopy, path)

			if err := dfsHelper(dp, pathCopy); err != nil {
				return err
			}
		}
		sortedCmps = append(sortedCmps, componentToConfig[name])
		return nil
	}

	for _, c := range components {
		if _, ok := visited[c.Name]; !ok {
			var path []string
			if err := dfsHelper(c.Name, path); err != nil {
				return nil, err
			}
		}
	}

	return sortedCmps, nil
}

// A Config describes the configuration of a robot.
type Config struct {
	ConfigFilePath string
	Cloud          *Cloud                      `json:"cloud,omitempty"`
	Remotes        []Remote                    `json:"remotes,omitempty"`
	Boards         []board.Config              `json:"boards,omitempty"`
	Components     []Component                 `json:"components,omitempty"`
	Processes      []pexec.ProcessConfig       `json:"processes,omitempty"`
	Functions      []functionvm.FunctionConfig `json:"functions,omitempty"`
}

// Ensure ensures all parts of the config are valid and sorts components based on what they depend on.
func (c *Config) Ensure(fromCloud bool) error {
	if c.Cloud != nil {
		if err := c.Cloud.Validate("cloud", fromCloud); err != nil {
			return err
		}
	}

	for idx, config := range c.Remotes {
		if err := config.Validate(fmt.Sprintf("%s.%d", "remotes", idx)); err != nil {
			return err
		}
	}

	for idx, config := range c.Boards {
		if err := config.Validate(fmt.Sprintf("%s.%d", "boards", idx)); err != nil {
			return err
		}
	}

	for idx, config := range c.Components {
		if err := config.Validate(fmt.Sprintf("%s.%d", "components", idx)); err != nil {
			return err
		}
	}

	if len(c.Components) > 0 {
		srtCmps, err := SortComponents(c.Components)
		if err != nil {
			return err
		}
		c.Components = srtCmps
	}

	for idx, config := range c.Processes {
		if err := config.Validate(fmt.Sprintf("%s.%d", "processes", idx)); err != nil {
			return err
		}
	}

	for idx, config := range c.Functions {
		if err := config.Validate(fmt.Sprintf("%s.%d", "functions", idx)); err != nil {
			return err
		}
	}

	return nil
}

// FindComponent finds a particular component by name.
func (c Config) FindComponent(name string) *Component {
	for _, cmp := range c.Components {
		if cmp.Name == name {
			return &cmp
		}
	}
	return nil
}

// A Remote describes a remote robot that should be integrated.
type Remote struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Prefix  bool   `json:"prefix"`
	Parent  string `json:"parent"`
}

// Validate ensures all parts of the config are valid.
func (config *Remote) Validate(path string) error {
	if config.Name == "" {
		return utils.NewConfigValidationFieldRequiredError(path, "name")
	}
	if config.Address == "" {
		return utils.NewConfigValidationFieldRequiredError(path, "address")
	}
	return nil
}

// A Cloud describes how to configure a robot controlled by the
// cloud.
// The cloud source could be anything that supports http.
// URL is constructed as $Path?id=ID and secret is put in a http header.
type Cloud struct {
	ID               string        `json:"id"`
	Secret           string        `json:"secret"`
	Self             string        `json:"self"`
	SignalingAddress string        `json:"signaling_address"`
	Path             string        `json:"path,omitempty"`    // optional, defaults to viam cloud otherwise
	LogPath          string        `json:"logPath,omitempty"` // optional, defaults to viam cloud otherwise
	RefreshInterval  time.Duration `json:"refresh_interval,omitempty"`
}

// Validate ensures all parts of the config are valid.
func (config *Cloud) Validate(path string, fromCloud bool) error {
	if config.ID == "" {
		return utils.NewConfigValidationFieldRequiredError(path, "id")
	}
	if fromCloud {
		if config.Self == "" {
			return utils.NewConfigValidationFieldRequiredError(path, "self")
		}
	} else {
		if config.Secret == "" {
			return utils.NewConfigValidationFieldRequiredError(path, "secret")
		}
	}
	if config.RefreshInterval == 0 {
		config.RefreshInterval = 10 * time.Second
	}
	return nil
}
