//go:build !linux

package ibcli

type globalConfigGroupInfo struct {
	Name string
	GID  int
}

var globalConfigEffectiveUserIDFunc = func() int {
	return -1
}

func globalConfigSupported() bool {
	return false
}

func requireGlobalConfigRoot() error {
	return nil
}

func lookupGlobalConfigGroup(groupName string) (globalConfigGroupInfo, error) {
	return globalConfigGroupInfo{}, cliError("--global-config is only supported on Linux")
}

func chownPathGroup(path string, group globalConfigGroupInfo) error {
	return cliError("--global-config is only supported on Linux")
}
