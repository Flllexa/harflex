package tools

import "os"

func openReadOnlyPlatform(root *os.Root, path string) (*os.File, error) {
	return root.OpenFile(path, os.O_RDONLY, 0)
}
