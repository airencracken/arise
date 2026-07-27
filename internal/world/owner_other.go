//go:build !unix

package world

import "os"

func fileOwner(os.FileInfo) (int, int) {
	return -1, -1
}
