//go:build windows

package orchestrator

func isPrivileged() bool {
	return true
}
