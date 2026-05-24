//go:build !windows

package ibcli

import "log/syslog"

func writeAuditSyslog(line []byte) error {
	writer, err := syslog.New(syslog.LOG_INFO|syslog.LOG_AUTH, "ib")
	if err != nil {
		return err
	}
	defer writer.Close()
	return writer.Info(string(line))
}
