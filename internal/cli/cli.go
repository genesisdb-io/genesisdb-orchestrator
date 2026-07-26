package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/genesisdb-io/genesisdb-orchestrator/internal/orchestrator"
	"github.com/genesisdb-io/genesisdb-orchestrator/internal/updater"
	"golang.org/x/term"
)

const (
	brandColor = "\x1b[1;38;2;0;181;212m"
	colorReset = "\x1b[0m"
	usage      = `GenesisDB orchestrator

Usage:
  genesisdb init
  genesisdb shutdown
  genesisdb create
  genesisdb create <name> --auth-token <token> [--license-key <key>]
  genesisdb stop <name>
  genesisdb delete <name>
  genesisdb update [--check]
  genesisdb version

Commands:
  init      Initialize and start the proxy and all instances
  shutdown  Stop the proxy and all GenesisDB instances
  create    Create an instance interactively or with command-line options
  stop      Stop an instance without deleting its data
  delete    Delete an instance and its data permanently
  update    Check for or install the latest GenesisDB CLI release
  version   Print the application version
`
)

// Run executes the GenesisDB CLI with the supplied arguments and build version.
func Run(args []string, version string) error {
	if len(args) == 0 {
		fmt.Print(styledUsage(os.Stdout))
		return nil
	}

	var updateResult <-chan updater.Info
	switch args[0] {
	case "init", "shutdown", "create", "stop", "delete":
		updateResult = updater.CheckAsync(version)
	}
	defer func() {
		if updateResult == nil {
			return
		}
		select {
		case info, ok := <-updateResult:
			if ok && info.Available {
				printUpdateNotice(info)
			}
		default:
		}
	}()

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(styledUsage(os.Stdout))
		return nil
	case "version", "--version":
		fmt.Println(version)
		return nil
	case "update":
		return runUpdate(args[1:], version)
	case "init":
		if len(args) != 1 {
			return errors.New("init takes no arguments")
		}
		return orchestrator.New().Init()
	case "shutdown":
		if len(args) != 1 {
			return errors.New("shutdown takes no arguments")
		}
		return orchestrator.New().Shutdown()
	case "create":
		name, token, license, err := parseCreate(args[1:])
		if err != nil {
			return err
		}
		name, token, license, err = completeCreateWizard(name, token, license)
		if err != nil {
			return err
		}
		return orchestrator.New().Create(name, token, license)
	case "stop", "delete":
		if len(args) != 2 {
			return fmt.Errorf("usage: genesisdb %s <name>", args[0])
		}
		if err := orchestrator.ValidateName(args[1]); err != nil {
			return err
		}
		app := orchestrator.New()
		if args[0] == "stop" {
			return app.Stop(args[1])
		}
		return app.Delete(args[1])
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], styledUsage(os.Stderr))
	}
}

func runUpdate(args []string, version string) error {
	if len(args) > 1 {
		return errors.New("usage: genesisdb update [--check]")
	}
	if len(args) == 1 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Print(`GenesisDB self-update

Usage:
  genesisdb update           Download and install the latest release
  genesisdb update --check   Check for a newer release without installing it
`)
			return nil
		case "--check":
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			info, err := updater.Check(ctx, version, false)
			if err != nil {
				return fmt.Errorf("check for update: %w", err)
			}
			if info.Current == "" || info.Current == "dev" || info.Current == "0.0.0" {
				fmt.Println("GenesisDB development build 0.0.0 cannot self-update.")
				return nil
			}
			if !info.Available {
				fmt.Printf("GenesisDB %s is up to date.\n", info.Current)
				return nil
			}
			fmt.Printf("GenesisDB %s -> %s is available.\nRelease: %s\nRun `genesisdb update` to install it.\n", info.Current, info.Latest, info.URL)
			return nil
		default:
			return fmt.Errorf("unknown update option %q", args[0])
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return updater.Install(ctx, version, os.Stdout)
}

func printUpdateNotice(info updater.Info) {
	title := "Update available"
	if colorEnabled(os.Stderr) {
		title = brandColor + title + colorReset
	}
	fmt.Fprintf(os.Stderr, "%s: GenesisDB %s -> %s. Run `genesisdb update`.\n", title, info.Current, info.Latest)
}

func styledUsage(output *os.File) string {
	if !colorEnabled(output) {
		return usage
	}

	styled := strings.Replace(usage, "GenesisDB orchestrator", brandColor+"GenesisDB orchestrator"+colorReset, 1)
	styled = strings.Replace(styled, "Usage:", brandColor+"Usage:"+colorReset, 1)
	return strings.Replace(styled, "Commands:", brandColor+"Commands:"+colorReset, 1)
}

func parseCreate(args []string) (name, token, license string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--auth-token":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", "", errors.New("--auth-token requires a value")
			}
			token = args[i]
		case "--license-key":
			i++
			if i >= len(args) {
				return "", "", "", errors.New("--license-key requires a value; use an empty string for the free license")
			}
			license = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", "", fmt.Errorf("unknown option %q", args[i])
			}
			if name != "" {
				return "", "", "", errors.New("create accepts exactly one instance name")
			}
			name = args[i]
		}
	}
	if name != "" {
		if err := orchestrator.ValidateName(name); err != nil {
			return "", "", "", err
		}
	}
	return name, token, license, nil
}

func completeCreateWizard(name, token, license string) (string, string, string, error) {
	if name != "" && token != "" {
		return name, token, license, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", "", "", errors.New("missing create arguments in a non-interactive terminal; provide <name> and --auth-token")
	}

	title := "Create a GenesisDB instance"
	if colorEnabled(os.Stdout) {
		title = brandColor + title + colorReset
	}
	fmt.Fprintln(os.Stdout, title)

	if name == "" {
		var err error
		name, err = readVisible("  Name: ", false)
		if err != nil {
			return "", "", "", err
		}
		if err := orchestrator.ValidateName(name); err != nil {
			return "", "", "", err
		}
	}
	if token == "" {
		var err error
		token, err = readVisible("  Auth token: ", false)
		if err != nil {
			return "", "", "", err
		}
	}
	if license == "" {
		var err error
		license, err = readSecret("  License key (optional): ", true)
		if err != nil {
			return "", "", "", err
		}
	}
	return name, token, license, nil
}

func readVisible(prompt string, allowEmpty bool) (string, error) {
	fmt.Fprint(os.Stdout, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read value: %w", err)
	}
	value := strings.TrimSpace(line)
	if value == "" && !allowEmpty {
		return "", errors.New("value cannot be empty")
	}
	return value, nil
}

func readSecret(prompt string, allowEmpty bool) (string, error) {
	fmt.Fprint(os.Stdout, prompt)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stdout)
	if err != nil {
		return "", fmt.Errorf("read secret: %w", err)
	}
	if len(value) == 0 && !allowEmpty {
		return "", errors.New("value cannot be empty")
	}
	return string(value), nil
}

func colorEnabled(output *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := output.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func commandOutput(w io.Writer, message string) {
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintln(w, message)
}
