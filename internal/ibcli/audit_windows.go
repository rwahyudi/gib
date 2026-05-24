//go:build windows

package ibcli

import "golang.org/x/sys/windows/svc/eventlog"

func writeAuditWindowsEventLog(line []byte) error {
	log, err := eventlog.Open("ib")
	if err != nil {
		if installErr := eventlog.InstallAsEventCreate("ib", eventlog.Info|eventlog.Warning|eventlog.Error); installErr != nil {
			return err
		}
		log, err = eventlog.Open("ib")
		if err != nil {
			return err
		}
	}
	defer log.Close()
	return log.Info(1, string(line))
}
