// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Tetragon

package program

import (
	"os"

	"github.com/cilium/tetragon/pkg/logger"
	"github.com/cilium/tetragon/pkg/logger/logfields"
)

// rodataConfigPin tracks the shared rodata config map pin.
// No locking needed: sensor load/unload is serialized by the sensor manager.
var rodataConfigPin = struct {
	path string
	refs int
}{}

func acquireRodataConfigPin(pinPath string) {
	rodataConfigPin.path = pinPath
	rodataConfigPin.refs++
}

func releaseRodataConfigPin(unpin bool) {
	if !unpin {
		return
	}

	rodataConfigPin.refs--
	if rodataConfigPin.refs > 0 {
		return
	}

	pinPath := rodataConfigPin.path
	rodataConfigPin.path = ""

	if err := os.Remove(pinPath); err != nil && !os.IsNotExist(err) {
		logger.GetLogger().Warn("Failed to unpin rodata config map", "map", pinPath, logfields.Error, err)
	}
}
