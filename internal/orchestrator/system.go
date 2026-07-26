package orchestrator

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const hostsMarker = "# genesisdb-local-cli"

func hostsPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "etc", "hosts")
	}
	return "/etc/hosts"
}

func updateHost(host string, add bool) error {
	path := hostsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read hosts file: %w", err)
	}
	newline := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		fields := strings.Fields(line)
		managedHost := len(fields) >= 3 && fields[len(fields)-2] == "#" && fields[len(fields)-1] == "genesisdb-local-cli"
		if managedHost && len(fields) >= 2 && fields[1] == host {
			continue
		}
		kept = append(kept, line)
	}
	if add {
		for len(kept) > 0 && kept[len(kept)-1] == "" {
			kept = kept[:len(kept)-1]
		}
		kept = append(kept, "127.0.0.1 "+host+" "+hostsMarker, "")
	}
	result := strings.Join(kept, newline)
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(path, []byte(result), 0); err != nil {
			return fmt.Errorf("write hosts file (run from an Administrator terminal): %w", err)
		}
		return nil
	}

	tmp, err := os.CreateTemp("", "genesisdb-hosts-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(result); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if isPrivileged() {
		return copyFileContents(tmpPath, path)
	}
	return runExternal("sudo", "cp", tmpPath, path)
}

func copyFileContents(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(data)
	return err
}

func trustCertificate(caPath, stateDir string) error {
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(ca))
	marker := filepath.Join(stateDir, "trusted-ca.sha256")
	if current, _ := os.ReadFile(marker); strings.TrimSpace(string(current)) == hash {
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
		args := []string{"security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", caPath}
		if !isPrivileged() {
			args = append([]string{"sudo"}, args...)
		}
		if err := runExternal(args[0], args[1:]...); err != nil {
			return err
		}
	case "linux":
		if _, err := exec.LookPath("update-ca-certificates"); err == nil {
			destination := "/usr/local/share/ca-certificates/genesisdb-local.crt"
			if err := privilegedCopy(caPath, destination); err != nil {
				return err
			}
			if err := privilegedRun("update-ca-certificates"); err != nil {
				return err
			}
		} else if _, err := exec.LookPath("trust"); err == nil {
			if err := privilegedRun("trust", "anchor", caPath); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("cannot trust CA: install update-ca-certificates or p11-kit trust")
		}
	case "windows":
		if err := runExternal("certutil", "-addstore", "-f", "Root", caPath); err != nil {
			return fmt.Errorf("trust CA (run from an Administrator terminal): %w", err)
		}
	default:
		return fmt.Errorf("unsupported operating system %s", runtime.GOOS)
	}
	return os.WriteFile(marker, []byte(hash+"\n"), 0o600)
}

func privilegedCopy(src, dst string) error {
	if runtime.GOOS != "windows" && !isPrivileged() {
		return runExternal("sudo", "cp", src, dst)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func privilegedRun(name string, args ...string) error {
	if runtime.GOOS != "windows" && !isPrivileged() {
		args = append([]string{name}, args...)
		name = "sudo"
	}
	return runExternal(name, args...)
}

func runExternal(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}
