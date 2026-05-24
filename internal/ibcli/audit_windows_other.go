//go:build !windows

package ibcli

func writeAuditWindowsEventLog([]byte) error {
	return cliError("Windows Event Log audit logging is only supported on Windows")
}
