package ibcli

import (
	"os"
	"path/filepath"
)

type configScope string

const (
	localConfigScope  configScope = "local"
	globalConfigScope configScope = "global"
)

var (
	lookupGlobalConfigGroupFunc = lookupGlobalConfigGroup
	chownPathGroupFunc          = chownPathGroup
)

type configLocation struct {
	Scope   configScope
	Dir     string
	File    string
	KeyFile string
}

func (a *App) ensureConfigPathDefaults() {
	if a.LocalConfigDir == "" && a.ConfigDir != "" {
		a.LocalConfigDir = a.ConfigDir
	}
	if a.LocalConfigFile == "" && a.ConfigFile != "" {
		a.LocalConfigFile = a.ConfigFile
	}
	if a.LocalConfigKeyFile == "" && a.ConfigKeyFile != "" {
		a.LocalConfigKeyFile = a.ConfigKeyFile
	}
	if a.GlobalConfigDir == "" {
		a.GlobalConfigDir = globalConfigDir
	}
	if a.GlobalConfigFile == "" {
		a.GlobalConfigFile = filepath.Join(a.GlobalConfigDir, configFileName)
	}
	if a.GlobalConfigKeyFile == "" {
		a.GlobalConfigKeyFile = filepath.Join(a.GlobalConfigDir, configKeyFileName)
	}
	if a.ConfigDir == "" && a.LocalConfigDir != "" {
		a.ConfigDir = a.LocalConfigDir
		a.ConfigFile = a.LocalConfigFile
		a.ConfigKeyFile = a.LocalConfigKeyFile
		a.configScope = localConfigScope
	}
}

func (a *App) localConfigLocation() configLocation {
	a.ensureConfigPathDefaults()
	return configLocation{
		Scope:   localConfigScope,
		Dir:     a.LocalConfigDir,
		File:    a.LocalConfigFile,
		KeyFile: a.LocalConfigKeyFile,
	}
}

func (a *App) globalConfigLocation() configLocation {
	a.ensureConfigPathDefaults()
	return configLocation{
		Scope:   globalConfigScope,
		Dir:     a.GlobalConfigDir,
		File:    a.GlobalConfigFile,
		KeyFile: a.GlobalConfigKeyFile,
	}
}

func (a *App) currentConfigLocation() configLocation {
	a.ensureConfigPathDefaults()
	scope := a.configScope
	if scope == "" {
		scope = localConfigScope
	}
	return configLocation{
		Scope:   scope,
		Dir:     a.ConfigDir,
		File:    a.ConfigFile,
		KeyFile: a.ConfigKeyFile,
	}
}

func (a *App) useConfigLocation(location configLocation) {
	a.ConfigDir = location.Dir
	a.ConfigFile = location.File
	a.ConfigKeyFile = location.KeyFile
	a.configScope = location.Scope
	if location.Scope != globalConfigScope {
		a.globalConfigGroup = ""
	}
}

func (a *App) activeConfigIsGlobal() bool {
	return a.currentConfigLocation().Scope == globalConfigScope
}

func (a *App) withConfigLocation(location configLocation, fn func() error) error {
	original := a.currentConfigLocation()
	originalGroup := a.globalConfigGroup
	a.useConfigLocation(location)
	err := fn()
	a.useConfigLocation(original)
	a.globalConfigGroup = originalGroup
	return err
}

func (a *App) readConfigLocations() []configLocation {
	locations := []configLocation{}
	if globalConfigSupported() {
		locations = append(locations, a.globalConfigLocation())
	}
	return append(locations, a.localConfigLocation())
}

func (a *App) useDefaultConfigLocation() (bool, error) {
	merged, err := a.readMergedConfig(false)
	if err != nil || len(merged.Profiles) == 0 {
		return false, err
	}
	location, ok := merged.ProfileLocations[merged.DefaultProfile]
	if !ok {
		return false, nil
	}
	a.activateConfigLocation(location, merged.FileData[location.Scope].Settings)
	return true, nil
}

func (a *App) activateConfigLocation(location configLocation, settings ConfigSettings) {
	a.useConfigLocation(location)
	if location.Scope == globalConfigScope && settings.GlobalGroup != "" {
		a.globalConfigGroup = settings.GlobalGroup
	}
}

func (a *App) prepareGlobalConfigGroup(groupName string) (string, error) {
	info, err := lookupGlobalConfigGroupFunc(groupName)
	if err != nil {
		return "", err
	}
	a.globalConfigGroup = info.Name
	return info.Name, nil
}

func (a *App) activeGlobalConfigGroup() (globalConfigGroupInfo, error) {
	groupName := a.globalConfigGroup
	if groupName == "" {
		return globalConfigGroupInfo{}, cliError("global config group is missing; run: ib config new --global-config")
	}
	return lookupGlobalConfigGroupFunc(groupName)
}

func (a *App) protectConfigDirForScope(strict bool) error {
	if !a.activeConfigIsGlobal() {
		return protectConfigDir(a.ConfigDir)
	}
	group, err := a.activeGlobalConfigGroup()
	if err != nil {
		if strict {
			return err
		}
		return nil
	}
	if err := chownPathGroupFunc(a.ConfigDir, group); err != nil {
		if strict {
			return err
		}
	}
	if err := os.Chmod(a.ConfigDir, 0o2770); err != nil {
		if strict {
			return err
		}
	}
	return nil
}

func (a *App) protectConfigFileForScope(strict bool) error {
	return a.protectFileForScope(a.ConfigFile, 0o640, strict)
}

func (a *App) protectConfigKeyFileForScope(strict bool) error {
	return a.protectFileForScope(a.ConfigKeyFile, 0o640, strict)
}

func (a *App) protectCacheFileForScope(strict bool) error {
	if !a.activeConfigIsGlobal() {
		return protectPrivateFile(a.cachePath())
	}
	return a.protectFileForScope(a.cachePath(), 0o660, strict)
}

func (a *App) protectFileForScope(path string, globalMode os.FileMode, strict bool) error {
	if !a.activeConfigIsGlobal() {
		return protectPrivateFile(path)
	}
	group, err := a.activeGlobalConfigGroup()
	if err != nil {
		if strict {
			return err
		}
		return nil
	}
	if err := chownPathGroupFunc(path, group); err != nil {
		if strict {
			return err
		}
	}
	if err := os.Chmod(path, globalMode); err != nil {
		if strict {
			return err
		}
	}
	return nil
}
