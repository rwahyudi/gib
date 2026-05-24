//go:build windows

package ibcli

func writeAuditSyslog([]byte) error {
	return cliError("syslog audit logging is not supported on Windows")
}
