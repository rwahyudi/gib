//go:build windows

package ibcli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var protectedWindowsPaths sync.Map

func (a *App) encryptCurrentPassword(password string) (string, error) {
	in := dataBlobFromBytes([]byte(password))
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer freeDataBlob(out)
	return encryptedWindowsDPAPIPrefix + base64.RawURLEncoding.EncodeToString(bytesFromDataBlob(out)), nil
}

func decryptWindowsDPAPIPassword(password string) (string, error) {
	token := strings.TrimPrefix(password, encryptedWindowsDPAPIPrefix)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}
	in := dataBlobFromBytes(raw)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer freeDataBlob(out)
	return string(bytesFromDataBlob(out)), nil
}

func credentialProtectionDescription() string {
	return "encrypted at rest with Windows DPAPI; legacy enc:v1 profiles use the key file until rewritten"
}

func protectConfigDir(path string) error {
	_ = protectWindowsPath(path)
	return nil
}

func protectPrivateFile(path string) error {
	_ = protectWindowsPath(path)
	return nil
}

func sessionBaseDir(kind string) string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "ib", "sessions", kind)
}

func processParentPID(pid int) int {
	return 0
}

func dataBlobFromBytes(data []byte) windows.DataBlob {
	if len(data) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
}

func bytesFromDataBlob(blob windows.DataBlob) []byte {
	if blob.Data == nil || blob.Size == 0 {
		return nil
	}
	raw := unsafe.Slice(blob.Data, int(blob.Size))
	return append([]byte(nil), raw...)
}

func freeDataBlob(blob windows.DataBlob) {
	if blob.Data != nil {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
	}
}

func protectWindowsPath(path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return nil
	}
	if _, ok := protectedWindowsPaths.Load(path); ok {
		return nil
	}
	userSID, err := currentUserSID()
	if err != nil {
		return err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	entries := []windows.EXPLICIT_ACCESS{
		fullControlForSID(userSID),
		fullControlForSID(systemSID),
		fullControlForSID(adminSID),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return err
	}
	protectedWindowsPaths.Store(path, struct{}{})
	return nil
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

func fullControlForSID(sid *windows.SID) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
