//go:build !windows

package ibcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) encryptCurrentPassword(password string) (string, error) {
	key, err := a.getOrCreateConfigKey()
	if err != nil {
		return "", err
	}
	return encryptFernet(key, password)
}

func decryptWindowsDPAPIPassword(password string) (string, error) {
	return "", cliError("Windows DPAPI password token cannot be decrypted on this platform")
}

func credentialProtectionDescription() string {
	return "encrypted at rest with a local key file"
}

func protectConfigDir(path string) error {
	if err := os.Chmod(path, 0o700); err != nil {
		info, statErr := os.Stat(path)
		if statErr == nil && info.IsDir() && info.Mode().Perm() == 0o700 {
			return nil
		}
		return err
	}
	return nil
}

func protectPrivateFile(path string) error {
	return os.Chmod(path, 0o600)
}

func sessionBaseDir(kind string) string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir != "" {
		if info, err := os.Stat(runtimeDir); err == nil && info.IsDir() {
			return filepath.Join(runtimeDir, "ib", kind)
		}
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("ib-%d", os.Getuid()), kind)
}

func processParentPID(pid int) int {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "PPid:" {
			parent, _ := strconv.Atoi(fields[1])
			return parent
		}
	}
	return 0
}
