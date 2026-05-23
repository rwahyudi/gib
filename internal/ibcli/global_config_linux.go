//go:build linux

package ibcli

import (
	"os"
	"os/user"
	"strconv"
	"strings"
)

type globalConfigGroupInfo struct {
	Name string
	GID  int
}

func globalConfigSupported() bool {
	return true
}

func lookupGlobalConfigGroup(groupName string) (globalConfigGroupInfo, error) {
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return globalConfigGroupInfo{}, cliError("global config group is required")
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return globalConfigGroupInfo{}, cliError("Linux group %q was not found", groupName)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return globalConfigGroupInfo{}, cliError("Linux group %q has invalid gid %q", group.Name, group.Gid)
	}
	return globalConfigGroupInfo{Name: group.Name, GID: gid}, nil
}

func chownPathGroup(path string, group globalConfigGroupInfo) error {
	return os.Chown(path, -1, group.GID)
}
