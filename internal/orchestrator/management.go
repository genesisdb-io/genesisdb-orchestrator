package orchestrator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Instance is a managed GenesisDB Docker container.
type Instance struct {
	Name      string
	Container string
	URL       string
	Running   bool
}

// Status is returned by GenesisDB's GET /api/v1/status endpoint.
type Status struct {
	Engine struct {
		Version string `json:"version"`
		Edition string `json:"edition"`
		Channel string `json:"channel"`
	} `json:"engine"`
	System struct {
		OS      string `json:"os"`
		Arch    string `json:"arch"`
		CPU     CPU    `json:"cpu"`
		Memory  Usage  `json:"memory"`
		Storage Usage  `json:"storage"`
	} `json:"system"`
	License struct {
		Status     string      `json:"status"`
		ValidUntil interface{} `json:"validUntil"`
	} `json:"license"`
	Events struct {
		Count       int   `json:"count"`
		Subjects    int   `json:"subjects"`
		Types       int   `json:"types"`
		StorageSize int64 `json:"storageSize"`
	} `json:"events"`
}

// CPU describes available and currently used CPU cores.
type CPU struct {
	AvailableCores int64 `json:"availableCores"`
	UsedCores      int64 `json:"usedCores"`
}

// Usage describes total, used, and available bytes.
type Usage struct {
	Total     uint64 `json:"total"`
	Max       uint64 `json:"max"`
	Used      uint64 `json:"used"`
	Available uint64 `json:"available"`
}

// Instances lists all managed GenesisDB containers, including stopped ones.
func (a *Orchestrator) Instances() ([]Instance, error) {
	if err := dockerAvailable(); err != nil {
		return nil, err
	}
	names, err := managedInstances()
	if err != nil {
		return nil, err
	}
	instances := make([]Instance, 0, len(names))
	for _, container := range names {
		_, running, err := containerState(container)
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(container, "genesisdb-local-")
		if label, err := docker("inspect", "--format", "{{ index .Config.Labels \"io.genesisdb.local.name\" }}", container); err == nil && strings.TrimSpace(label) != "" {
			name = strings.TrimSpace(label)
		}
		instances = append(instances, Instance{
			Name:      name,
			Container: container,
			URL:       "https://" + name + ".genesisdb.local",
			Running:   running,
		})
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].Name < instances[j].Name })
	return instances, nil
}

// ProxyRunning reports whether the managed reverse proxy is running.
func (a *Orchestrator) ProxyRunning() (bool, error) {
	if err := dockerAvailable(); err != nil {
		return false, err
	}
	exists, running, err := containerState(proxyContainer)
	return exists && running, err
}

// Start starts one stopped GenesisDB instance.
func (a *Orchestrator) Start(name string) error {
	if err := requireProxy(); err != nil {
		return err
	}
	exists, running, err := containerState(instanceContainer(name))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("instance %q does not exist", name)
	}
	if running {
		return nil
	}
	if _, err := docker("start", instanceContainer(name)); err != nil {
		return fmt.Errorf("start %q: %w", name, err)
	}
	return nil
}

// StopInstance stops one instance without writing command output.
func (a *Orchestrator) StopInstance(name string) error {
	if err := requireProxy(); err != nil {
		return err
	}
	exists, running, err := containerState(instanceContainer(name))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("instance %q does not exist", name)
	}
	if !running {
		return nil
	}
	if _, err := docker("stop", instanceContainer(name)); err != nil {
		return fmt.Errorf("stop %q: %w", name, err)
	}
	return nil
}

// DeleteInstance removes an instance and its data without writing command output.
func (a *Orchestrator) DeleteInstance(name string) error {
	if err := requireProxy(); err != nil {
		return err
	}
	exists, _, err := containerState(instanceContainer(name))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("instance %q does not exist", name)
	}
	if _, err := docker("rm", "-f", "-v", instanceContainer(name)); err != nil {
		return fmt.Errorf("delete container: %w", err)
	}
	if _, err := docker("volume", "rm", instanceVolume(name)); err != nil {
		return fmt.Errorf("delete data volume: %w", err)
	}
	if err := os.Remove(a.sitePath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := a.reloadProxy(); err != nil {
		return err
	}
	if err := updateHost(name+".genesisdb.local", false); err != nil {
		return fmt.Errorf("instance deleted, but hosts-file cleanup failed: %w", err)
	}
	return nil
}

// Status fetches detailed status information from one running instance.
func (a *Orchestrator) Status(ctx context.Context, name string) (Status, error) {
	var status Status
	response, err := a.apiRequest(ctx, name, http.MethodGet, "/api/v1/status", nil)
	if err != nil {
		return status, err
	}
	defer response.Close()
	if err := json.NewDecoder(response).Decode(&status); err != nil {
		return status, fmt.Errorf("decode status: %w", err)
	}
	return status, nil
}

// ExportBackup writes a verified API response to destination atomically.
func (a *Orchestrator) ExportBackup(ctx context.Context, name, destination string) error {
	response, err := a.apiRequest(ctx, name, http.MethodGet, "/api/v1/backup/create", nil)
	if err != nil {
		return err
	}
	defer response.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	temporary := destination + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	if _, err := io.Copy(file, response); err != nil {
		file.Close()
		_ = os.Remove(temporary)
		return fmt.Errorf("write backup: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("finish backup: %w", err)
	}
	return nil
}

// ImportBackup restores a JSON backup into an empty GenesisDB instance.
func (a *Orchestrator) ImportBackup(ctx context.Context, name, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer file.Close()
	response, err := a.apiRequest(ctx, name, http.MethodPost, "/api/v1/backup/restore", file)
	if err != nil {
		return err
	}
	defer response.Close()
	_, err = io.Copy(io.Discard, response)
	return err
}

func (a *Orchestrator) apiRequest(ctx context.Context, name, method, path string, body io.Reader) (io.ReadCloser, error) {
	exists, running, err := containerState(instanceContainer(name))
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("instance %q does not exist", name)
	}
	if !running {
		return nil, fmt.Errorf("instance %q is stopped", name)
	}
	token, err := instanceAuthToken(name)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+name+".genesisdb.local"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client, err := a.apiClient()
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("%s returned HTTP %d: %s", path, response.StatusCode, strings.TrimSpace(string(message)))
	}
	return response.Body, nil
}

func (a *Orchestrator) apiClient() (*http.Client, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	ca, err := os.ReadFile(filepath.Join(a.certsDir, "ca.pem"))
	if err != nil {
		return nil, fmt.Errorf("read local CA: %w", err)
	}
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("load local CA certificate")
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
		}},
	}, nil
}

func instanceAuthToken(name string) (string, error) {
	output, err := docker("inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", instanceContainer(name))
	if err != nil {
		return "", fmt.Errorf("read credentials for %q: %w", name, err)
	}
	for _, line := range strings.Split(output, "\n") {
		if token, ok := strings.CutPrefix(line, "GENESISDB_AUTH_TOKEN="); ok {
			return token, nil
		}
	}
	return "", fmt.Errorf("instance %q has no auth token", name)
}
