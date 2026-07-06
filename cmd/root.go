package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/yegor-usoltsev/MagicHop/internal/bluetooth"
	"github.com/yegor-usoltsev/MagicHop/internal/config"
	"github.com/yegor-usoltsev/MagicHop/internal/coordinator"
	"github.com/yegor-usoltsev/MagicHop/internal/daemon"
	"github.com/yegor-usoltsev/MagicHop/internal/install"
	"github.com/yegor-usoltsev/MagicHop/internal/protocol"
	appruntime "github.com/yegor-usoltsev/MagicHop/internal/runtime"
	"github.com/yegor-usoltsev/MagicHop/internal/upgrade"
)

type cli struct {
	Coordinator coordinatorCmd `cmd:"" help:"Run the embedded NATS coordinator."`
	Daemon      daemonCmd      `cmd:"" help:"Run the macOS client daemon."`
	Claim       claimCmd       `cmd:"" help:"Claim a Bluetooth device."`
	Status      statusCmd      `cmd:"" help:"Show local and coordinator status."`
	Config      configCmd      `cmd:"" help:"Manage client configuration."`
	Install     installCmd     `cmd:"" help:"Install helper integrations."`
	Uninstall   uninstallCmd   `cmd:"" help:"Uninstall helper integrations."`
	Upgrade     upgradeCmd     `cmd:"" help:"Download and install the latest release."`
	Version     versionCmd     `cmd:"" help:"Print version."`
}

type runContext struct {
	Context context.Context
}

type exitError struct {
	code int
}

func (e exitError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}

func commandResult(code int) error {
	if code == 0 {
		return nil
	}
	return exitError{code: code}
}

func Run(args []string) int {
	appruntime.SetupLogger()
	ctx, cancel := appruntime.SignalContext()
	defer cancel()

	var app cli
	exitCode := 0
	exitCalled := false
	parser, err := newParser(&app, func(code int) {
		exitCode = code
		exitCalled = true
	})
	if err != nil {
		slog.Error("failed to build cli parser", "err", err)
		return 1
	}
	if isRootHelp(args) {
		_, _ = parser.Parse(args)
		return 0
	}
	parsed, err := parser.Parse(args)
	if err != nil {
		parser.FatalIfErrorf(err)
		if exitCalled && exitCode == 0 {
			return 0
		}
		return 2
	}
	if exitCalled && exitCode == 0 {
		return 0
	}
	if err := parsed.Run(&runContext{Context: ctx}); err != nil {
		var exit exitError
		if errors.As(err, &exit) {
			return exit.code
		}
		slog.Error("command failed", "err", err)
		return 1
	}
	return 0
}

func isRootHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h")
}

func newParser(app *cli, exit func(int)) (*kong.Kong, error) {
	parser, err := kong.New(
		app,
		kong.Name("magichop"),
		kong.Description("Coordinate Bluetooth device claims between Macs."),
		kong.Exit(exit),
		kong.ShortUsageOnError(),
	)
	if err != nil {
		return nil, fmt.Errorf("create kong parser: %w", err)
	}
	return parser, nil
}

type coordinatorCmd struct{}

func (c *coordinatorCmd) Run(ctx *runContext) error {
	return commandResult(runCoordinator(ctx.Context))
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

type clientOptions struct {
	Config string `type:"path" help:"Config path."`
}

type daemonCmd struct {
	clientOptions
}

func (c *daemonCmd) Run(ctx *runContext) error {
	return commandResult(runDaemon(ctx.Context, c.Config))
}

func runDaemon(ctx context.Context, configPath string) int {
	cfg, bt, err := clientDeps(configPath)
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

type claimCmd struct {
	clientOptions
	Device string `arg:"" optional:"" help:"Device alias or Bluetooth MAC address."`
}

func (c *claimCmd) Run(ctx *runContext) error {
	return commandResult(runClaim(ctx.Context, c.Config, c.Device))
}

func runClaim(ctx context.Context, configPath, device string) int {
	cfg, bt, err := clientDeps(configPath)
	if err != nil {
		slog.Error("failed to load claim", "err", err)
		return 1
	}
	result, err := (daemon.Client{Config: cfg, Bluetooth: bt}).Claim(ctx, device)
	if err != nil {
		slog.Error("claim failed", "err", err)
		return 1
	}
	for _, ack := range result.Acks {
		if ack.OK {
			fmt.Printf("%s: released\n", ack.Node) //nolint:forbidigo // CLI output
			continue
		}
		fmt.Printf("%s: release failed: %s\n", ack.Node, ack.Error) //nolint:forbidigo // CLI output
	}
	if result.Connect.OK {
		fmt.Println("local connect: ok") //nolint:forbidigo // CLI output
		return 0
	}
	fmt.Printf("local connect: failed: %s\n", result.Connect.Message()) //nolint:forbidigo // CLI output
	return 1
}

type statusCmd struct {
	clientOptions
}

func (c *statusCmd) Run(ctx *runContext) error {
	return commandResult(runStatus(ctx.Context, c.Config))
}

func runStatus(ctx context.Context, configPath string) int {
	cfg, bt, err := clientDeps(configPath)
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

type configCmd struct {
	Init configInitCmd `cmd:"" help:"Create a client config file."`
}

type configInitCmd struct {
	Config         string   `type:"path" help:"Config path."`
	Force          bool     `help:"Overwrite existing config."`
	NodeName       string   `name:"node-name" help:"Node name."`
	CoordinatorURL string   `name:"coordinator-url" help:"Coordinator NATS URL."`
	AuthToken      string   `name:"auth-token" help:"NATS auth token."`
	DefaultDevice  string   `name:"default-device" help:"Default device alias."`
	Devices        []string `name:"device" help:"Device alias=address. May be repeated."`
}

func (c *configInitCmd) Run(_ *runContext) error {
	return commandResult(runConfigInit(*c))
}

func runConfigInit(opts configInitCmd) int {
	path, err := configPath(opts.Config)
	if err != nil {
		slog.Error("failed to resolve config path", "err", err)
		return 1
	}
	cfg := config.InitClientConfig()
	if opts.NodeName != "" {
		cfg.NodeName = opts.NodeName
	}
	if opts.CoordinatorURL != "" {
		cfg.CoordinatorURL = opts.CoordinatorURL
	}
	if opts.AuthToken != "" {
		cfg.AuthToken = opts.AuthToken
	}
	devices, err := parseDeviceFlags(opts.Devices)
	if err != nil {
		slog.Error("invalid config arguments", "err", err)
		return 2
	}
	if len(devices) > 0 {
		cfg.Devices = devices
	}
	if opts.DefaultDevice != "" {
		cfg.DefaultDevice = opts.DefaultDevice
	}
	if err := config.WriteClient(path, cfg, opts.Force); err != nil {
		slog.Error("failed to write config", "err", err)
		return 1
	}
	fmt.Printf("Config written: %s\n", path) //nolint:forbidigo // CLI output
	return 0
}

type installCmd struct {
	Mac     installMacCmd     `cmd:"" help:"Install the macOS LaunchAgent."`
	Raycast installRaycastCmd `cmd:"" help:"Generate a Raycast Script Command."`
}

type installMacCmd struct{}

func (c *installMacCmd) Run(_ *runContext) error {
	return commandResult(runInstallMac())
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

type installRaycastCmd struct {
	Dir    string `required:"" type:"path" help:"Raycast Script Commands directory."`
	Binary string `type:"path" help:"Override magichop binary path."`
}

func (c *installRaycastCmd) Run(_ *runContext) error {
	return commandResult(runInstallRaycast(*c))
}

func runInstallRaycast(opts installRaycastCmd) int {
	bin := opts.Binary
	if bin == "" {
		var err error
		bin, err = executablePath()
		if err != nil {
			slog.Error("failed to resolve executable path", "err", err)
			return 1
		}
	}
	path, err := install.WriteRaycastScript(opts.Dir, bin)
	if err != nil {
		slog.Error("raycast install failed", "err", err)
		return 1
	}
	fmt.Printf("Raycast script: %s\n", path) //nolint:forbidigo // CLI output
	return 0
}

type uninstallCmd struct {
	Mac uninstallMacCmd `cmd:"" help:"Uninstall the macOS LaunchAgent."`
}

type uninstallMacCmd struct {
	PurgeConfig bool `name:"purge-config" help:"Remove config."`
}

func (c *uninstallMacCmd) Run(_ *runContext) error {
	return commandResult(runUninstallMac(c.PurgeConfig))
}

func runUninstallMac(purgeConfig bool) int {
	paths, err := install.DefaultPaths()
	if err != nil {
		slog.Error("failed to resolve install paths", "err", err)
		return 1
	}
	if err := install.UninstallMac(paths, purgeConfig); err != nil {
		slog.Error("uninstall failed", "err", err)
		return 1
	}
	fmt.Println("MagicHop uninstalled.") //nolint:forbidigo // CLI output
	if !purgeConfig {
		fmt.Printf("Config preserved: %s\n", paths.ConfigPath) //nolint:forbidigo // CLI output
	}
	return 0
}

type upgradeCmd struct{}

func (c *upgradeCmd) Run(ctx *runContext) error {
	return commandResult(runUpgrade(ctx.Context))
}

func runUpgrade(ctx context.Context) int {
	path, err := executablePath()
	if err != nil {
		slog.Error("failed to resolve executable path", "err", err)
		return 1
	}
	result, err := upgrade.Run(ctx, upgrade.Options{
		CurrentVersion: appruntime.Version,
		ExecutablePath: path,
	})
	if err != nil {
		slog.Error("upgrade failed", "err", err)
		return 1
	}
	if !result.Updated {
		fmt.Printf("Already up to date: %s\n", result.CurrentVersion) //nolint:forbidigo // CLI output
		return 0
	}
	fmt.Printf("Updated %s -> %s\n", result.CurrentVersion, result.LatestVersion) //nolint:forbidigo // CLI output
	return 0
}

type versionCmd struct{}

func (c *versionCmd) Run(_ *runContext) error {
	fmt.Println(appruntime.Version) //nolint:forbidigo // CLI output
	return nil
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

func parseDeviceFlags(values []string) (map[string]string, error) {
	devices := make(map[string]string, len(values))
	for _, value := range values {
		alias, address, ok := strings.Cut(value, "=")
		if !ok || alias == "" || address == "" {
			return nil, fmt.Errorf("device must be alias=address")
		}
		devices[alias] = protocol.NormalizeBluetoothAddress(address)
	}
	return devices, nil
}
