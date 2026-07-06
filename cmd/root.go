package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yegor-usoltsev/MagicHop/internal/bluetooth"
	"github.com/yegor-usoltsev/MagicHop/internal/config"
	"github.com/yegor-usoltsev/MagicHop/internal/coordinator"
	"github.com/yegor-usoltsev/MagicHop/internal/daemon"
	"github.com/yegor-usoltsev/MagicHop/internal/install"
	"github.com/yegor-usoltsev/MagicHop/internal/protocol"
	appruntime "github.com/yegor-usoltsev/MagicHop/internal/runtime"
)

func Run(args []string) int {
	appruntime.SetupLogger()
	if len(args) == 0 {
		usage()
		return 2
	}
	ctx, cancel := appruntime.SignalContext()
	defer cancel()

	switch args[0] {
	case "coordinator":
		return runCoordinator(ctx)
	case "daemon":
		return runDaemon(ctx, args[1:])
	case "claim":
		return runClaim(ctx, args[1:])
	case "status":
		return runStatus(ctx, args[1:])
	case "config":
		return runConfig(args[1:])
	case "install":
		return runInstall(args[1:])
	case "uninstall":
		return runUninstall(args[1:])
	case "version":
		fmt.Println(appruntime.Version) //nolint:forbidigo // CLI output
		return 0
	default:
		slog.Error("unknown command", "command", args[0])
		usage()
		return 2
	}
}

func runCoordinator(ctx context.Context) int {
	cfg, err := config.NewServerConfigFromEnv()
	if err != nil {
		slog.Error("failed to load coordinator config", "err", err)
		return 1
	}
	if err := coordinator.New(cfg).Run(ctx); err != nil {
		slog.Error("coordinator failed", "err", err)
		return 1
	}
	return 0
}

func runDaemon(ctx context.Context, args []string) int {
	opts, err := parseClientArgs("daemon", args, 0)
	if err != nil {
		slog.Error("invalid daemon arguments", "err", err)
		return 2
	}
	cfg, bt, err := clientDeps(opts.configPath)
	if err != nil {
		slog.Error("failed to load daemon", "err", err)
		return 1
	}
	if err := (daemon.Client{Config: cfg, Bluetooth: bt}).Run(ctx); err != nil {
		slog.Error("daemon failed", "err", err)
		return 1
	}
	return 0
}

func runClaim(ctx context.Context, args []string) int {
	opts, err := parseClientArgs("claim", args, 1)
	if err != nil {
		slog.Error("invalid claim arguments", "err", err)
		return 2
	}
	cfg, bt, err := clientDeps(opts.configPath)
	if err != nil {
		slog.Error("failed to load claim", "err", err)
		return 1
	}
	result, err := (daemon.Client{Config: cfg, Bluetooth: bt}).Claim(ctx, opts.device)
	if err != nil {
		slog.Error("claim failed", "err", err)
		return 1
	}
	for _, ack := range result.Acks {
		if ack.OK {
			fmt.Printf("%s: disconnected\n", ack.Node) //nolint:forbidigo // CLI output
			continue
		}
		fmt.Printf("%s: disconnect failed: %s\n", ack.Node, ack.Error) //nolint:forbidigo // CLI output
	}
	if result.Connect.OK {
		fmt.Println("local connect: ok") //nolint:forbidigo // CLI output
		return 0
	}
	fmt.Printf("local connect: failed: %s\n", result.Connect.Message()) //nolint:forbidigo // CLI output
	return 1
}

func runStatus(ctx context.Context, args []string) int {
	opts, err := parseClientArgs("status", args, 0)
	if err != nil {
		slog.Error("invalid status arguments", "err", err)
		return 2
	}
	cfg, bt, err := clientDeps(opts.configPath)
	if err != nil {
		slog.Error("failed to load status", "err", err)
		return 1
	}
	connected, nodes, err := (daemon.Client{Config: cfg, Bluetooth: bt}).Status(ctx)
	if err != nil {
		slog.Error("status failed", "err", err)
		return 1
	}
	fmt.Printf("Node: %s\n", cfg.NodeName)                //nolint:forbidigo // CLI output
	fmt.Printf("Coordinator: %s\n", cfg.CoordinatorURL)   //nolint:forbidigo // CLI output
	fmt.Printf("Default device: %s\n", cfg.DefaultDevice) //nolint:forbidigo // CLI output
	fmt.Printf("Connected: %t\n", connected)              //nolint:forbidigo // CLI output
	fmt.Printf("Known nodes: %d\n", len(nodes))           //nolint:forbidigo // CLI output
	for _, node := range nodes {
		fmt.Printf("- %s seen %s\n", node.Node, node.SeenAt.Format(time.RFC3339)) //nolint:forbidigo // CLI output
	}
	return 0
}

func runConfig(args []string) int {
	if len(args) == 0 || args[0] != "init" {
		slog.Error("unknown config command")
		return 2
	}
	fs := flag.NewFlagSet("config init", flag.ContinueOnError)
	pathFlag := fs.String("config", "", "config path")
	force := fs.Bool("force", false, "overwrite existing config")
	nodeName := fs.String("node-name", "", "node name")
	url := fs.String("coordinator-url", "", "coordinator URL")
	token := fs.String("auth-token", "", "NATS auth token")
	defaultDevice := fs.String("default-device", "", "default device alias")
	devices := deviceFlags{}
	fs.Var(&devices, "device", "device alias=address")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	path, err := configPath(*pathFlag)
	if err != nil {
		slog.Error("failed to resolve config path", "err", err)
		return 1
	}
	cfg := config.InitClientConfig()
	if *nodeName != "" {
		cfg.NodeName = *nodeName
	}
	if *url != "" {
		cfg.CoordinatorURL = *url
	}
	if *token != "" {
		cfg.AuthToken = *token
	}
	if len(devices) > 0 {
		cfg.Devices = devices
	}
	if *defaultDevice != "" {
		cfg.DefaultDevice = *defaultDevice
	}
	if err := config.WriteClient(path, cfg, *force); err != nil {
		slog.Error("failed to write config", "err", err)
		return 1
	}
	fmt.Printf("Config written: %s\n", path) //nolint:forbidigo // CLI output
	return 0
}

func runInstall(args []string) int {
	if len(args) == 0 {
		slog.Error("install target is required")
		return 2
	}
	switch args[0] {
	case "mac":
		return runInstallMac()
	case "raycast":
		return runInstallRaycast(args[1:])
	default:
		slog.Error("unknown install target", "target", args[0])
		return 2
	}
}

func runInstallMac() int {
	paths, err := install.DefaultPaths()
	if err != nil {
		slog.Error("failed to resolve install paths", "err", err)
		return 1
	}
	binary, err := executablePath()
	if err != nil {
		slog.Error("failed to resolve executable path", "err", err)
		return 1
	}
	if err := install.Mac(binary, paths); err != nil {
		slog.Error("install failed", "err", err)
		return 1
	}
	fmt.Printf("LaunchAgent: %s\n", paths.PlistPath)                    //nolint:forbidigo // CLI output
	fmt.Println("Recommended binary path: ~/.local/bin/magichop")       //nolint:forbidigo // CLI output
	fmt.Println("Raycast: magichop install raycast --dir <script-dir>") //nolint:forbidigo // CLI output
	return 0
}

func runInstallRaycast(args []string) int {
	fs := flag.NewFlagSet("install raycast", flag.ContinueOnError)
	dir := fs.String("dir", "", "Raycast Script Commands directory")
	binary := fs.String("binary", "", "override magichop binary path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		slog.Error("raycast directory is required")
		return 2
	}
	bin := *binary
	if bin == "" {
		var err error
		bin, err = executablePath()
		if err != nil {
			slog.Error("failed to resolve executable path", "err", err)
			return 1
		}
	}
	path, err := install.WriteRaycastScript(*dir, bin)
	if err != nil {
		slog.Error("raycast install failed", "err", err)
		return 1
	}
	fmt.Printf("Raycast script: %s\n", path) //nolint:forbidigo // CLI output
	return 0
}

func runUninstall(args []string) int {
	if len(args) == 0 || args[0] != "mac" {
		slog.Error("unknown uninstall target")
		return 2
	}
	fs := flag.NewFlagSet("uninstall mac", flag.ContinueOnError)
	purgeConfig := fs.Bool("purge-config", false, "remove config")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	paths, err := install.DefaultPaths()
	if err != nil {
		slog.Error("failed to resolve install paths", "err", err)
		return 1
	}
	if err := install.UninstallMac(paths, *purgeConfig); err != nil {
		slog.Error("uninstall failed", "err", err)
		return 1
	}
	fmt.Println("MagicHop uninstalled.") //nolint:forbidigo // CLI output
	if !*purgeConfig {
		fmt.Printf("Config preserved: %s\n", paths.ConfigPath) //nolint:forbidigo // CLI output
	}
	return 0
}

func clientDeps(path string) (config.ClientConfig, bluetooth.Backend, error) {
	cfgPath, err := configPath(path)
	if err != nil {
		return config.ClientConfig{}, nil, fmt.Errorf("resolve config path: %w", err)
	}
	cfg, err := config.LoadClient(cfgPath)
	if err != nil {
		return config.ClientConfig{}, nil, fmt.Errorf("load config: %w", err)
	}
	bt, err := bluetooth.NewBlueutil()
	if err != nil {
		return config.ClientConfig{}, nil, fmt.Errorf("create bluetooth backend: %w", err)
	}
	return cfg, bt, nil
}

type clientArgs struct {
	configPath string
	device     string
}

func parseClientArgs(command string, args []string, maxDevices int) (clientArgs, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config path")
	if err := fs.Parse(args); err != nil {
		return clientArgs{}, fmt.Errorf("parse %s args: %w", command, err)
	}
	devices := fs.Args()
	if len(devices) > maxDevices {
		return clientArgs{}, fmt.Errorf("%s accepts at most %d device args", command, maxDevices)
	}
	opts := clientArgs{configPath: *configPath}
	if len(devices) == 1 {
		opts.device = devices[0]
	}
	return opts, nil
}

func configPath(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	path, err := config.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("default config path: %w", err)
	}
	return path, nil
}

func executablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find executable: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return path, nil
}

type deviceFlags map[string]string

func (d deviceFlags) String() string {
	values := make([]string, 0, len(d))
	for alias, address := range d {
		values = append(values, alias+"="+address)
	}
	return strings.Join(values, ",")
}

func (d deviceFlags) Set(value string) error {
	alias, address, ok := strings.Cut(value, "=")
	if !ok || alias == "" || address == "" {
		return fmt.Errorf("device must be alias=address")
	}
	d[alias] = protocol.NormalizeBluetoothAddress(address)
	return nil
}

func usage() {
	fmt.Println(`Usage: magichop <command> [options]`) //nolint:forbidigo // CLI output
	fmt.Println()                                      //nolint:forbidigo // CLI output
	fmt.Println(`Commands:`)                           //nolint:forbidigo // CLI output
	fmt.Println(`  coordinator`)                       //nolint:forbidigo // CLI output
	fmt.Println(`  daemon [--config path]`)            //nolint:forbidigo // CLI output
	fmt.Println(`  claim [--config path] [device]`)    //nolint:forbidigo // CLI output
	fmt.Println(`  status [--config path]`)            //nolint:forbidigo // CLI output
	fmt.Println(`  config init [options]`)             //nolint:forbidigo // CLI output
	fmt.Println(`  install mac`)                       //nolint:forbidigo // CLI output
	fmt.Println(`  install raycast --dir dir`)         //nolint:forbidigo // CLI output
	fmt.Println(`  uninstall mac [--purge-config]`)    //nolint:forbidigo // CLI output
}
