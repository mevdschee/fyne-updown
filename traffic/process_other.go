//go:build !linux && !windows && !darwin

package traffic

import "errors"

func startProcessMonitor() (ProcessMonitor, error) {
	return nil, errors.New("per-process traffic is not supported on this platform")
}
