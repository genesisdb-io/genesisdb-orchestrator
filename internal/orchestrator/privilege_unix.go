//go:build !windows

package orchestrator

import "os"

func isPrivileged() bool {
	return os.Geteuid() == 0
}
