package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/yegor-usoltsev/magichop/internal/config"
	"github.com/yegor-usoltsev/magichop/internal/coordinator"
	"github.com/yegor-usoltsev/magichop/internal/daemon"
	"github.com/yegor-usoltsev/magichop/internal/install"
	"github.com/yegor-usoltsev/magichop/internal/protocol"
	"github.com/yegor-usoltsev/magichop/internal/release"
	"github.com/yegor-usoltsev/magichop/internal/upgrade"
)

type CLI struct {
	Server    ServerCmd    `cmd:"" help:"Run the coordinator server."`
	Daemon    DaemonCmd    `cmd:"" help:"Run the local MagicHop daemon."`
	Claim     ClaimCmd     `cmd:"" help:"Claim a device through the local daemon."`
	Release   ReleaseCmd   `cmd:"" help:"Release a device locally."`
	Status    StatusCmd    `cmd:"" help:"Show daemon and device status."`
	Devices   DevicesCmd   `cmd:"" help:"List or scan devices."`
	Doctor    DoctorCmd    `cmd:"" help:"Check local setup."`
	Install   InstallCmd   `cmd:"" help:"Install local services or Raycast scripts."`
	Uninstall UninstallCmd `cmd:"" help:"Uninstall local services."`
	Edit      EditCmd      `cmd:"" help:"Edit the config file."`
	Upgrade   UpgradeCmd   `cmd:"" help:"Upgrade the current binary."`
	Version   VersionCmd   `cmd:"" help:"Print build version."`
}

func Run(args []string) int {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("magichop"), kong.Description("Magic peripheral handoff utility"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx.BindTo(context.Background(), (*context.Context)(nil))
	if err := ctx.Run(); err != nil {
		return exitCode(err)
	}
	return 0
}

type ServerCmd struct {
	Host      string `help:"Host to bind." default:"0.0.0.0" env:"MAGICHOP_HOST"`
	Port      int    `help:"Port to bind." default:"4222" env:"MAGICHOP_PORT"`
	AuthToken string `help:"Shared auth token." env:"MAGICHOP_AUTH_TOKEN"`
}

func (c ServerCmd) Run(ctx context.Context) error {
	if err := coordinator.Run(ctx, coordinator.Options{Host: c.Host, Port: c.Port, AuthToken: c.AuthToken}); err != nil {
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}

type DaemonCmd struct {
	Config string `help:"Config path." default:"~/.config/magichop/config.json"`
}

func (c DaemonCmd) Run(ctx context.Context) error {
	if err := daemon.Run(ctx, daemon.Options{ConfigPath: expandHome(c.Config)}); err != nil {
		return fmt.Errorf("daemon failed: %w", err)
	}
	return nil
}

type ClaimCmd struct {
	Device  string        `arg:"" optional:"" name:"device-or-address"`
	JSON    bool          `help:"Print JSON output."`
	Timeout time.Duration `help:"Whole claim timeout." default:"11s"`
}

func (c ClaimCmd) Run(ctx context.Context) error {
	req := protocol.LocalRequest{Type: protocol.LocalClaim, Device: c.Device, TimeoutMS: c.Timeout.Milliseconds()}
	clientID, err := protocol.NewRequestID()
	if err != nil {
		return err
	}
	if resolved, err := resolveCLIInput(c.Device); err == nil && resolved != "" {
		req.Device = resolved
	}
	_ = persistClientRequestID(clientID)
	req.ClientRequestID = clientID
	var accepted protocol.Accepted
	var final protocol.FinalResult
	if err := callDaemon(ctx, req, []any{&accepted, &final}); err != nil {
		return err
	}
	if c.JSON {
		if err := printJSON(final); err != nil {
			return err
		}
		if !final.OK {
			return runtimeErr(final.Error)
		}
		return nil
	}
	if final.OK {
		fmt.Printf("claimed %s request_id=%s\n", final.Device, final.RequestID)
		return nil
	}
	fmt.Printf("claim failed request_id=%s error=%s\n", final.RequestID, final.Error)
	return runtimeErr(final.Error)
}

type ReleaseCmd struct {
	Device string `arg:"" optional:"" name:"device-or-address"`
	JSON   bool   `help:"Print JSON output."`
}

func (c ReleaseCmd) Run(ctx context.Context) error {
	device := c.Device
	if resolved, err := resolveCLIInput(c.Device); err == nil && resolved != "" {
		device = resolved
	}
	var out protocol.ReleaseResult
	if err := callDaemon(ctx, protocol.LocalRequest{Type: protocol.LocalRelease, Device: device}, []any{&out}); err != nil {
		return err
	}
	if c.JSON {
		if err := printJSON(out); err != nil {
			return err
		}
		if !out.OK {
			return runtimeErr(out.Error)
		}
		return nil
	}
	if out.OK {
		fmt.Printf("released %s\n", out.Device)
		return nil
	}
	fmt.Printf("release failed error=%s\n", out.Error)
	return runtimeErr(out.Error)
}

func resolveCLIInput(input string) (string, error) {
	cfg, _, err := config.Load(config.LoadOptions{})
	if err != nil {
		return input, err
	}
	_, address, err := config.ResolveDevice(cfg, input)
	if err != nil {
		return input, err
	}
	return address, nil
}

func persistClientRequestID(id string) error {
	dir := filepath.Join(os.TempDir(), "magichop-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "last-client-request-id"), []byte(id+"\n"), 0o600)
}

type StatusCmd struct {
	Device string `arg:"" optional:"" name:"device-or-address"`
	JSON   bool   `help:"Print JSON output."`
}

func (c StatusCmd) Run(ctx context.Context) error {
	var out protocol.StatusResult
	if err := callDaemon(ctx, protocol.LocalRequest{Type: protocol.LocalStatus, Device: c.Device}, []any{&out}); err != nil {
		return err
	}
	if c.JSON {
		return printJSON(out)
	}
	fmt.Printf("daemon=%s coordinator=%s device=%s last_error=%s\n", out.Daemon, out.Coordinator, out.Device, out.LastError)
	return nil
}

type DevicesCmd struct {
	JSON bool `help:"Print JSON output."`
	Scan bool `help:"Scan paired Bluetooth devices."`
}

func (c DevicesCmd) Run(ctx context.Context) error {
	var out protocol.DevicesResult
	if err := callDaemon(ctx, protocol.LocalRequest{Type: protocol.LocalDevices, Scan: c.Scan}, []any{&out}); err != nil {
		return err
	}
	if c.JSON {
		return printJSON(out)
	}
	for alias, address := range out.Devices {
		fmt.Printf("%s %s\n", alias, address)
	}
	return nil
}

type DoctorCmd struct {
	JSON bool `help:"Print JSON output."`
}

func (c DoctorCmd) Run(ctx context.Context) error {
	var out protocol.DoctorResult
	if err := callDaemon(ctx, protocol.LocalRequest{Type: protocol.LocalDoctor}, []any{&out}); err != nil {
		return err
	}
	if c.JSON {
		return printJSON(out)
	}
	for _, check := range out.Checks {
		fmt.Printf("%s ok=%t %s\n", check.Name, check.OK, check.Err)
	}
	return nil
}

type InstallCmd struct {
	Mac     InstallMacCmd     `cmd:"" help:"Install macOS LaunchAgent."`
	Raycast InstallRaycastCmd `cmd:"" help:"Install Raycast scripts."`
}

type InstallMacCmd struct {
	Config string `help:"Config path." default:"~/.config/magichop/config.json"`
}

func (c InstallMacCmd) Run(_ context.Context) error {
	binary, err := executablePath()
	if err != nil {
		return err
	}
	return install.Mac(expandHome(c.Config), binary)
}

type InstallRaycastCmd struct {
	Dir string `help:"Raycast script directory." default:"~/.local/raycast-scripts"`
}

func (c InstallRaycastCmd) Run(_ context.Context) error {
	cfg, _, err := config.Load(config.LoadOptions{})
	if err != nil {
		return usageErr(err)
	}
	binary, err := executablePath()
	if err != nil {
		return err
	}
	return install.Raycast(expandHome(c.Dir), cfg, binary)
}

type UninstallCmd struct {
	Mac UninstallMacCmd `cmd:"" help:"Uninstall macOS LaunchAgent."`
}

type UninstallMacCmd struct{}

func (UninstallMacCmd) Run(_ context.Context) error {
	return install.UninstallMac()
}

type EditCmd struct{}

func (EditCmd) Run(_ context.Context) error {
	return install.EditConfig(config.DefaultPath())
}

type UpgradeCmd struct {
	Check   bool   `help:"Only check latest available version."`
	Version string `help:"Specific version to install."`
	Yes     bool   `help:"Do not prompt."`
}

func (c UpgradeCmd) Run(ctx context.Context) error {
	return upgrade.Run(ctx, upgrade.Options{Check: c.Check, Version: c.Version, Yes: c.Yes})
}

type VersionCmd struct {
	JSON bool `help:"Print JSON output."`
}

func (c VersionCmd) Run(_ context.Context) error {
	info := release.Info()
	if c.JSON {
		return printJSON(info)
	}
	fmt.Printf("magichop %s commit=%s date=%s\n", info["version"], info["commit"], info["date"])
	return nil
}

func callDaemon(ctx context.Context, request protocol.LocalRequest, replies []any) error {
	conn, err := net.DialTimeout("unix", daemon.SocketPath(), 2*time.Second)
	if err != nil {
		return daemonUnavailableErr(err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := protocol.EncodeLine(conn, request); err != nil {
		return err
	}
	dec := json.NewDecoder(conn)
	for _, reply := range replies {
		if err := dec.Decode(reply); err != nil {
			if errors.Is(err, io.EOF) {
				return runtimeErr(protocol.ErrInvalidResponse)
			}
			return err
		}
	}
	return nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type codedError struct {
	code int
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }

func usageErr(err error) error             { return codedError{code: 2, err: err} }
func daemonUnavailableErr(err error) error { return codedError{code: 3, err: err} }
func permissionErr(err error) error        { return codedError{code: 4, err: err} }
func runtimeErr(msg string) error {
	if msg == "" {
		msg = "runtime failure"
	}
	return codedError{code: 1, err: errors.New(msg)}
}

func exitCode(err error) int {
	var coded codedError
	if errors.As(err, &coded) {
		fmt.Fprintln(os.Stderr, coded.err)
		return coded.code
	}
	if errors.Is(err, os.ErrPermission) {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if len(path) == 1 {
		return home
	}
	if path[1] == '/' {
		return home + path[1:]
	}
	return path
}

func executablePath() (string, error) {
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			return abs, nil
		}
	}
	if strings.ContainsRune(os.Args[0], filepath.Separator) {
		return filepath.Abs(os.Args[0])
	}
	path, err := exec.LookPath(os.Args[0])
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}
