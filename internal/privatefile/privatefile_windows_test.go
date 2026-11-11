package privatefile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func setTestDACL(t *testing.T, path, sddl string) {
	t.Helper()

	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}

	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}

	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsPrivateCreationOverridesParentInheritance(t *testing.T) {
	dir := t.TempDir()
	setTestDACL(t, dir, "D:P(A;OICI;FA;;;WD)")
	path := filepath.Join(dir, "private.bin")

	file, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Check(path); err != nil {
		t.Fatalf("broad parent ACL was inherited: %v", err)
	}

	setTestDACL(t, path, "D:P(A;;FA;;;WD)")

	if err := Check(path); err == nil {
		t.Fatal("Everyone grant accepted as private")
	}
}

func TestWindowsPrivateCheckRejectsReparsePoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.bin")

	file, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("Windows account cannot create symlinks: %v", err)
	}

	if err := Check(link); err == nil {
		t.Fatal("reparse point accepted as a private file")
	}
}
