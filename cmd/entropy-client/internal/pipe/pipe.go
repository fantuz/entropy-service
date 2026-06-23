package pipe

import (
	"os"
)

func WriteStdout(data []byte) error {
	_, err := os.Stdout.Write(data)

	return err
}
