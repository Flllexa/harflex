//go:build !darwin

package application

import "errors"

var updaterNotWritable = errors.New("the app's folder is not writable")
