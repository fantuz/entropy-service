package device

import (
	"os"
)

func Write(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)

	return err
}

/*
type EntropyFile struct {
	server string
}

func (f *EntropyFile) Read(dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {

	resp, err := http.Get(f.server)
	if err != nil {
		return nil, syscall.EIO
	}

	defer resp.Body.Close()

	n, _ := io.ReadFull(resp.Body, dest)

	return fuse.ReadResultData(dest[:n]), 0
}
*/
