//go:build !linux

package ibcli

type globalConfigGroupInfo struct {
	Name string
	GID  int
}

func globalConfigSupported() bool {
	return false
}

func lookupGlobalConfigGroup(groupName string) (globalConfigGroupInfo, error) {
	return globalConfigGroupInfo{}, cliError("--global-config is only supported on Linux")
}

func chownPathGroup(path string, group globalConfigGroupInfo) error {
	return cliError("--global-config is only supported on Linux")
}
