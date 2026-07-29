package orchestrator

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	dockerNetwork  = "genesisdb-local"
	proxyContainer = "genesisdb-local-proxy"
	caddyImage     = "caddy:2-alpine"
	genesisImage   = "genesisdb/genesisdb:latest"
)

var validName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Orchestrator manages the local proxy and GenesisDB containers.
type Orchestrator struct {
	configDir string
	sitesDir  string
	certsDir  string
}

// New returns an orchestrator configured for the current user.
func New() *Orchestrator {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	config := filepath.Join(base, "genesisdb")
	return &Orchestrator{
		configDir: config,
		sitesDir:  filepath.Join(config, "caddy", "sites"),
		certsDir:  filepath.Join(config, "certs"),
	}
}

// ValidateName checks that an instance name is a valid local DNS label.
func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return errors.New("name must be a lowercase DNS label using letters, digits, and internal hyphens (maximum 63 characters)")
	}
	if name == "genesisdb" {
		return errors.New("name \"genesisdb\" is reserved")
	}
	return nil
}

// Init initializes the proxy and starts all managed containers.
func (a *Orchestrator) Init() error {
	if err := dockerAvailable(); err != nil {
		return err
	}
	if err := os.MkdirAll(a.sitesDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(a.certsDir, 0o700); err != nil {
		return err
	}
	if err := ensureCertificates(a.certsDir); err != nil {
		return err
	}
	if err := a.writeCaddyConfig(); err != nil {
		return err
	}
	if err := ensureNetwork(); err != nil {
		return err
	}
	if err := a.ensureProxy(); err != nil {
		return err
	}
	if err := waitForProxy(); err != nil {
		return err
	}
	started, err := startAllInstances()
	if err != nil {
		return err
	}
	if err := trustCertificate(filepath.Join(a.certsDir, "ca.pem"), a.configDir); err != nil {
		return fmt.Errorf("proxy started, but CA installation failed: %w", err)
	}
	if err := updateHost("genesisdb.local", true); err != nil {
		return fmt.Errorf("proxy started, but hosts-file update failed: %w", err)
	}
	if started == 0 {
		fmt.Println("GenesisDB initialized at https://genesisdb.local")
	} else {
		fmt.Printf("GenesisDB initialized and started %d instance(s)\n", started)
	}
	return nil
}

// Shutdown stops all managed containers while preserving their data.
func (a *Orchestrator) Shutdown() error {
	if err := dockerAvailable(); err != nil {
		return err
	}

	instances, err := managedInstances()
	if err != nil {
		return err
	}
	stopped := 0
	for _, name := range instances {
		_, running, err := containerState(name)
		if err != nil {
			return err
		}
		if running {
			if _, err := docker("stop", name); err != nil {
				return fmt.Errorf("stop %s: %w", name, err)
			}
			stopped++
		}
	}

	exists, running, err := containerState(proxyContainer)
	if err != nil {
		return err
	}
	if exists {
		label, err := docker("inspect", "--format", "{{ index .Config.Labels \"io.genesisdb.local.role\" }}", proxyContainer)
		if err != nil {
			return err
		}
		if strings.TrimSpace(label) != "proxy" {
			return fmt.Errorf("Docker container %q exists but is not managed by genesisdb", proxyContainer)
		}
		if running {
			if _, err := docker("stop", proxyContainer); err != nil {
				return fmt.Errorf("stop proxy: %w", err)
			}
			stopped++
		}
	}

	if stopped == 0 {
		fmt.Println("GenesisDB is already shut down")
	} else {
		fmt.Printf("Shut down GenesisDB (%d container(s) stopped)\n", stopped)
	}
	return nil
}

func startAllInstances() (int, error) {
	instances, err := managedInstances()
	if err != nil {
		return 0, err
	}
	started := 0
	for _, name := range instances {
		_, running, err := containerState(name)
		if err != nil {
			return started, err
		}
		if !running {
			if _, err := docker("start", name); err != nil {
				return started, fmt.Errorf("start %s: %w", name, err)
			}
			started++
		}
	}
	return started, nil
}

func managedInstances() ([]string, error) {
	output, err := docker("ps", "-a", "--filter", "label=io.genesisdb.local.role=instance", "--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("list GenesisDB instances: %w", err)
	}
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}
	return strings.Fields(output), nil
}

// Create creates and starts a GenesisDB instance.
func (a *Orchestrator) Create(name, token, license string) error {
	if err := requireProxy(); err != nil {
		return err
	}
	container := instanceContainer(name)
	exists, _, err := containerState(container)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("instance %q already exists", name)
	}

	volume := instanceVolume(name)
	args := []string{
		"run", "-d",
		"--name", container,
		"--network", dockerNetwork,
		"--restart", "unless-stopped",
		"--label", "io.genesisdb.local.role=instance",
		"--label", "io.genesisdb.local.name=" + name,
		"--mount", "type=volume,src=" + volume + ",dst=/data",
		"-e", "GENESISDB_AUTH_TOKEN=" + token,
		"-e", "GENESISDB_LICENSE=" + license,
		"-e", "GENESISDB_DATA_DIR=/data",
		"-e", "GENESISDB_TZ=UTC",
		"-e", "GENESISDB_METRICS=true",
		genesisImage,
	}
	if _, err := docker(args...); err != nil {
		return fmt.Errorf("create GenesisDB container: %w", err)
	}

	if err := a.writeSite(name); err != nil {
		a.removeInstanceResources(name)
		return err
	}
	if err := a.reloadProxy(); err != nil {
		os.Remove(a.sitePath(name))
		a.removeInstanceResources(name)
		return err
	}
	if err := updateHost(name+".genesisdb.local", true); err != nil {
		os.Remove(a.sitePath(name))
		_ = a.reloadProxy()
		a.removeInstanceResources(name)
		return fmt.Errorf("instance rolled back because the hosts file could not be updated: %w", err)
	}
	fmt.Printf("Created %q at https://%s.genesisdb.local\n", name, name)
	return nil
}

func (a *Orchestrator) removeInstanceResources(name string) {
	_, _ = docker("rm", "-f", instanceContainer(name))
	_, _ = docker("volume", "rm", instanceVolume(name))
}

// Stop stops one GenesisDB instance.
func (a *Orchestrator) Stop(name string) error {
	if err := a.StopInstance(name); err != nil {
		return err
	}
	fmt.Printf("Stopped %q\n", name)
	return nil
}

// Delete removes one GenesisDB instance and its data.
func (a *Orchestrator) Delete(name string) error {
	if err := a.DeleteInstance(name); err != nil {
		return err
	}
	fmt.Printf("Deleted %q and its data\n", name)
	return nil
}

func (a *Orchestrator) writeCaddyConfig() error {
	caddyfile := `{
	admin localhost:2019
	auto_https off
}

http://genesisdb.local {
	redir https://genesisdb.local{uri} permanent
}

https://genesisdb.local {
	tls /certs/server.pem /certs/server-key.pem
	respond "GenesisDB local proxy is running" 200
}

import /etc/caddy/sites/*.caddy
`
	if err := os.WriteFile(filepath.Join(a.configDir, "caddy", "Caddyfile"), []byte(caddyfile), 0o644); err != nil {
		return fmt.Errorf("write Caddy config: %w", err)
	}
	// Keep the import glob valid before the first instance is created.
	return os.WriteFile(filepath.Join(a.sitesDir, "00-empty.caddy"), []byte("# Instance routes are generated by genesisdb.\n"), 0o644)
}

func (a *Orchestrator) writeSite(name string) error {
	config := fmt.Sprintf(`http://%s.genesisdb.local {
	redir https://%s.genesisdb.local{uri} permanent
}

https://%s.genesisdb.local {
	tls /certs/server.pem /certs/server-key.pem
	reverse_proxy %s:8080
}
`, name, name, name, instanceContainer(name))
	return os.WriteFile(a.sitePath(name), []byte(config), 0o644)
}

func (a *Orchestrator) sitePath(name string) string {
	return filepath.Join(a.sitesDir, name+".caddy")
}

func (a *Orchestrator) ensureProxy() error {
	exists, running, err := containerState(proxyContainer)
	if err != nil {
		return err
	}
	if exists {
		label, err := docker("inspect", "--format", "{{ index .Config.Labels \"io.genesisdb.local.role\" }}", proxyContainer)
		if err != nil || strings.TrimSpace(label) != "proxy" {
			return fmt.Errorf("Docker container %q exists but is not managed by genesisdb", proxyContainer)
		}
		if !running {
			if _, err := docker("start", proxyContainer); err != nil {
				return fmt.Errorf("start proxy: %w", err)
			}
			return nil
		}
		return a.reloadProxy()
	}

	caddyDir := filepath.Join(a.configDir, "caddy")
	args := []string{
		"run", "-d",
		"--name", proxyContainer,
		"--network", dockerNetwork,
		"--restart", "unless-stopped",
		"--label", "io.genesisdb.local.role=proxy",
		"-p", "80:80", "-p", "443:443",
		"--mount", "type=bind,src=" + caddyDir + ",dst=/etc/caddy,readonly",
		"--mount", "type=bind,src=" + a.certsDir + ",dst=/certs,readonly",
		caddyImage,
	}
	if _, err := docker(args...); err != nil {
		return fmt.Errorf("start proxy (ensure ports 80 and 443 are free): %w", err)
	}
	return nil
}

func (a *Orchestrator) reloadProxy() error {
	if _, err := docker("exec", proxyContainer, "caddy", "reload", "--config", "/etc/caddy/Caddyfile"); err != nil {
		return fmt.Errorf("reload proxy: %w", err)
	}
	return nil
}

func requireProxy() error {
	if err := dockerAvailable(); err != nil {
		return err
	}
	exists, running, err := containerState(proxyContainer)
	if err != nil {
		return err
	}
	if !exists || !running {
		return errors.New("GenesisDB is not initialized or its proxy is stopped; run `genesisdb init`")
	}
	return nil
}

func ensureNetwork() error {
	if _, err := docker("network", "inspect", dockerNetwork); err == nil {
		return nil
	}
	if _, err := docker("network", "create", "--label", "io.genesisdb.local.role=network", dockerNetwork); err != nil {
		return fmt.Errorf("create Docker network: %w", err)
	}
	return nil
}

func waitForProxy() error {
	var last error
	for range 20 {
		if _, err := docker("exec", proxyContainer, "caddy", "validate", "--config", "/etc/caddy/Caddyfile"); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("proxy did not become ready: %w", last)
}

func dockerAvailable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("Docker CLI was not found in PATH")
	}
	if _, err := docker("info", "--format", "{{.ServerVersion}}"); err != nil {
		return fmt.Errorf("Docker is not running or is unavailable: %w", err)
	}
	return nil
}

func containerState(name string) (exists, running bool, err error) {
	output, err := docker("container", "inspect", "--format", "{{.State.Running}}", name)
	if err != nil {
		if strings.Contains(err.Error(), "No such container") {
			return false, false, nil
		}
		return false, false, err
	}
	return true, strings.TrimSpace(output) == "true", nil
}

func docker(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return stdout.String(), nil
}

func instanceContainer(name string) string { return "genesisdb-local-" + name }
func instanceVolume(name string) string    { return instanceContainer(name) + "-data" }
