// Copyright © 2022 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package buildah

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/containers/buildah/pkg/parse"
	"github.com/containers/common/pkg/config"
	"github.com/containers/storage/pkg/homedir"
	"github.com/containers/storage/pkg/unshare"
	"github.com/containers/storage/types"

	"github.com/labring/sealos/pkg/utils/file"
	"github.com/labring/sealos/pkg/utils/logger"
)

var (
	DefaultConfigFile                  string
	DefaultSignaturePolicyPath         = config.DefaultSignaturePolicyPath
	DefaultRootlessSignaturePolicyPath = "containers/policy.json"
	DefaultGraphRoot                   = "/var/lib/containers/storage"
	DefaultRegistriesFilePath          = "/etc/containers/registries.conf"
	DefaultRootlessRegistriesFilePath  = "containers/registries.conf"
)

func init() {
	_ = os.Setenv("TMPDIR", parse.GetTempDir())
	var err error
	DefaultConfigFile, err = types.DefaultConfigFile(unshare.IsRootless())
	if err != nil {
		logger.Fatal(err)
	}
	// config path
	if unshare.IsRootless() {
		configHome, err := homedir.GetConfigHome()
		if err != nil {
			logger.Fatal(err)
		}
		DefaultSignaturePolicyPath = filepath.Join(configHome, DefaultRootlessSignaturePolicyPath)
		DefaultRegistriesFilePath = filepath.Join(configHome, DefaultRootlessRegistriesFilePath)
	}

	// setters
	if unshare.IsRootless() {
		defaultSetters = append(defaultSetters,
			determineIfRootlessPackagePresent,
		)
	} else if isRunningInContainer() {
		defaultSetters = append(defaultSetters, func() error {
			deps := map[string][]string{"fuse-overlayfs": {"fuse-overlayfs"}}
			return determineIfPackagePresent(deps)
		})
	}

	defaultSetters = append(defaultSetters, maybeReexecUsingUserNamespace)
	defaultSetters = append(defaultSetters, configSetters...)
}

const defaultPolicy = `
{
    "default": [
        {
            "type": "insecureAcceptAnything"
        }
    ],
    "transports":
        {
            "docker-daemon":
                {
                    "": [{"type":"insecureAcceptAnything"}]
                }
        }
}
`

const defaultRegistries = `unqualified-search-registries = ["docker.io"]

[[registry]]
prefix = "docker.io/labring"
location = "docker.io/labring"
`

const (
	defaultRootStorageConf = `[storage]
  driver = "overlay"
  runroot = "/run/containers/storage"
  graphroot = "/var/lib/containers/storage"`
	defaultRootlessStorageConf = `[storage]
  driver = "overlay"
  runroot = "/run/user/%d"`
	mountProgramSnippet = `
  [storage.options]
    mount_program = "/bin/fuse-overlayfs"`
)

// todo: what if running by containerd?
func isRunningInContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

func setupContainerPolicy() error {
	return writeFileIfNotExists(DefaultSignaturePolicyPath, []byte(defaultPolicy))
}

func setupRegistriesFile() error {
	return writeFileIfNotExists(DefaultRegistriesFilePath, []byte(defaultRegistries))
}

func setupStorageConfigFile() error {
	var content string
	if unshare.IsRootless() {
		content = fmt.Sprintf(defaultRootlessStorageConf, unshare.GetRootlessUID())
	} else {
		content = defaultRootStorageConf
	}
	if unshare.IsRootless() || isRunningInContainer() {
		content += mountProgramSnippet
	}
	return writeFileIfNotExists(DefaultConfigFile, []byte(content))
}

func writeFileIfNotExists(filename string, data []byte) error {
	_, err := os.Stat(filename)
	if os.IsNotExist(err) {
		logger.Debug("create new buildah config %s cause it's not exist", filename)
		err = file.WriteFile(filename, data)
	}
	return err
}

func determineIfRootlessPackagePresent() error {
	deps := map[string][]string{"uidmap": {"newuidmap", "newgidmap"}, "fuse-overlayfs": {"fuse-overlayfs"}}
	if err := determineIfPackagePresent(deps); err != nil {
		return fmt.Errorf("%s or consider running in root mode", err.Error())
	}
	return nil
}

func determineIfPackagePresent(deps map[string][]string) error {
	for pkg, executables := range deps {
		for i := range executables {
			if _, err := exec.LookPath(executables[i]); err != nil {
				return fmt.Errorf("executable file '%s' not found in $PATH, install package '%s' first", executables[i], pkg)
			}
		}
	}
	return nil
}

func maybeReexecUsingUserNamespace() error {
	unshare.MaybeReexecUsingUserNamespace(false)
	return nil
}

type Setter func() error

var configSetters = []Setter{
	setupContainerPolicy,
	setupRegistriesFile,
	setupStorageConfigFile,
}

var defaultSetters = []Setter{}

func TrySetupWithDefaults(setters ...Setter) error {
	if len(setters) == 0 {
		setters = defaultSetters
	}
	for i := range setters {
		if err := setters[i](); err != nil {
			return err
		}
	}
	return nil
}
