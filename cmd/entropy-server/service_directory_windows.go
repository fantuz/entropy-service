package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// An administrative parent directory is an installation prerequisite. Reject
// junctions in the selected path rather than following them during provisioning.
func checkServicePath(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		name, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}

		attributes, err := windows.GetFileAttributes(name)
		if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return err
		}

		if err == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("service state path must not traverse a reparse point: %s", current)
		}

		if parent := filepath.Dir(current); parent == current {
			return nil
		}
	}
}

func prepareServiceDirectory(path string, sid *windows.SID) error {
	if err := checkServicePath(path); err != nil {
		return err
	}

	sd, err := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid.String() + ")")
	if err != nil {
		return err
	}

	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(name, &attributes); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}

	return checkServiceDirectory(path, sid)
}

func checkServiceDirectory(path string, sid *windows.SID) error {
	if err := checkServicePath(path); err != nil {
		return err
	}

	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}

	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("service state must be a directory without reparse points")
	}

	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}

	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}

	control, _, err := sd.Control()
	if err != nil {
		return err
	}

	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}

	if owner == nil || owner.String() != "S-1-5-32-544" || control&windows.SE_DACL_PROTECTED == 0 || acl == nil || acl.AceCount != 3 {
		return errors.New("service directory requires protected administrator ownership and exactly SYSTEM, Administrators and service-account grants; existing ACLs are never rewritten")
	}

	allowed := map[string]bool{"S-1-5-18": true, "S-1-5-32-544": true, sid.String(): true}

	for i := range uint32(acl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}

		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != windows.CONTAINER_INHERIT_ACE|windows.OBJECT_INHERIT_ACE || ace.Mask != 0x1f01ff {
			return errors.New("unsafe service-directory ACL; existing ACLs are never rewritten")
		}

		identity := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if !allowed[identity] {
			return errors.New("unexpected service-directory identity; existing ACLs are never rewritten")
		}

		delete(allowed, identity)
	}

	if len(allowed) != 0 {
		return os.ErrPermission
	}

	return nil
}

// Only public certificates and diagnostic logs receive administrator/SYSTEM read
// access. Private keys retain privatefile's single-account ACL for reuse checks.
func allowServiceDiagnostics(file *os.File, sid *windows.SID) error {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + sid.String() + ")(A;;FR;;;BA)(A;;FR;;;SY)")
	if err != nil {
		return err
	}

	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}

	return windows.SetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func allowServicePublicFile(path string, sid *windows.SID) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}

	file := os.NewFile(uintptr(handle), path)
	defer file.Close()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}

	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return errors.New("service certificate must be a regular file without reparse points")
	}

	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}

	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}

	if owner == nil || !owner.Equals(sid) {
		return errors.New("self-signed service certificate must belong to the service account")
	}

	return allowServiceDiagnostics(file, sid)
}
