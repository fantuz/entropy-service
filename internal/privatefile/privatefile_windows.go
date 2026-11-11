package privatefile

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Create applies the current user's protected DACL at creation, before any data
// is written. Unsupported filesystems fail closed; chmod is not an ACL policy.
func Create(path string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}

	sid := user.User.Sid.String()

	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}

	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}

	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.READ_CONTROL, windows.FILE_SHARE_READ,
		&attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "create private file", Path: path, Err: err}
	}

	file := os.NewFile(uintptr(handle), path)
	if err := checkFile(file); err != nil {
		info, statErr := file.Stat()
		closeErr := file.Close()

		current, currentErr := os.Lstat(path)
		if statErr == nil && currentErr == nil && os.SameFile(info, current) {
			err = errors.Join(err, os.Remove(path))
		}

		return nil, errors.Join(err, statErr, closeErr, currentErr)
	}

	return file, nil
}

// Check accepts a regular file owned by the current account with a protected,
// single-user allow ACL. It never relaxes or rewrites an existing file's ACL.
func Check(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}

	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return &os.PathError{Op: "inspect private file", Path: path, Err: err}
	}

	file := os.NewFile(uintptr(handle), path)
	defer file.Close()

	return checkFile(file)
}

func checkFile(file *os.File) error {
	handle := windows.Handle(file.Fd())

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}

	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return errors.New("private file must be regular, without reparse points")
	}

	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}

	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
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

	if owner == nil || !owner.Equals(user.User.Sid) || control&windows.SE_DACL_PROTECTED == 0 || acl == nil || acl.AceCount != 1 {
		return errors.New("private file requires a protected ACL owned by the current Windows account with one user-only grant")
	}

	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		return err
	}

	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(owner) {
		return errors.New("private file ACL must grant access only to its owner")
	}

	return nil
}
