# MagicHop

[![Build Status](https://github.com/yegor-usoltsev/magichop/actions/workflows/ci.yml/badge.svg)](https://github.com/yegor-usoltsev/magichop/actions)
[![Codecov](https://codecov.io/github/yegor-usoltsev/magichop/graph/badge.svg?token=5I7K9PUI0P)](https://codecov.io/github/yegor-usoltsev/magichop)
[![GitHub Release](https://img.shields.io/github/v/release/yegor-usoltsev/magichop?sort=semver)](https://github.com/yegor-usoltsev/magichop/releases)
[![Docker Image (docker.io)](https://img.shields.io/docker/v/yusoltsev/magichop?label=docker.io&sort=semver)](https://hub.docker.com/r/yusoltsev/magichop)
[![Docker Image (ghcr.io)](https://img.shields.io/docker/v/yusoltsev/magichop?label=ghcr.io&sort=semver)](https://github.com/yegor-usoltsev/magichop/pkgs/container/magichop)
[![Docker Image Size](https://img.shields.io/docker/image-size/yusoltsev/magichop?sort=semver&arch=amd64)](https://hub.docker.com/r/yusoltsev/magichop/tags)

MagicHop lets several Macs share Apple Magic peripherals, such as Magic Keyboard, Magic Mouse, and Magic Trackpad, without talking to each other directly. A coordinator runs embedded NATS, each Mac runs a local daemon, and `magichop claim` asks another Mac to release a peripheral before acquiring it locally.

Release means unpairing the peripheral from the current Mac. Acquire means clearing stale local pairing state, pairing, connecting, and verifying the connected state.

## Usage

### Coordinator

Run the coordinator on an always-on host:

```bash
docker run -d \
  --name magichop \
  --restart unless-stopped \
  -e MAGICHOP_AUTH_TOKEN=<shared-token> \
  -e MAGICHOP_HOST=0.0.0.0 \
  -e MAGICHOP_PORT=4222 \
  -p 4222:4222 \
  ghcr.io/yegor-usoltsev/magichop:latest
```

Or run the binary directly:

```bash
magichop server --host 0.0.0.0 --port 4222 --auth-token <shared-token>
```

### Macs

Put the release binary somewhere stable:

```bash
mkdir -p ~/.local/bin
cp magichop ~/.local/bin/magichop
```

Install `blueutil` and find paired Bluetooth addresses:

```bash
brew install blueutil
blueutil --paired
```

Create `~/.config/magichop/config.json`:

```json
{
  "node_name": "macbook-a",
  "coordinator_url": "nats://coordinator.local:4222",
  "auth_token": "CHANGE_ME",
  "default_device": "trackpad",
  "devices": {
    "keyboard": "aa:bb:cc:dd:ee:01",
    "trackpad": "aa:bb:cc:dd:ee:02"
  }
}
```

The config file contains the shared token and should be owner-only:

```bash
chmod 600 ~/.config/magichop/config.json
```

Install and start the macOS LaunchAgent:

```bash
magichop install mac
```

Run a daemon manually during development:

```bash
magichop daemon --config ~/.config/magichop/config.json
```

Claim the default peripheral:

```bash
magichop claim
```

Claim an alias or a raw Bluetooth address:

```bash
magichop claim keyboard
magichop claim trackpad
magichop claim aa-bb-cc-dd-ee-ff
```

Release locally without telling another Mac to acquire:

```bash
magichop release trackpad
```

Inspect local state:

```bash
magichop status
magichop devices
magichop devices --scan
magichop doctor
```

JSON output is available for user-facing daemon commands:

```bash
magichop claim --json
magichop release trackpad --json
magichop status --json
magichop devices --json
magichop doctor --json
```

## Raycast

Generate Raycast Script Commands:

```bash
magichop install raycast
```

Add `~/.local/raycast-scripts` in Raycast under Settings -> Extensions -> Script Commands -> Add Script Directory.

Generated scripts call `magichop claim <device>`. Raycast and other local clients never run `blueutil` directly.

## Environment Variables

Coordinator:

| Key | Type | Default | Required |
| --- | --- | --- | --- |
| `MAGICHOP_HOST` | `string` | `0.0.0.0` | No |
| `MAGICHOP_PORT` | `int` | `4222` | No |
| `MAGICHOP_AUTH_TOKEN` | `string` | | Yes |

Mac clients use `MAGICHOP_CONFIG` when it is set. Otherwise they read `~/.config/magichop/config.json`.

## Commands

```bash
magichop server --host 0.0.0.0 --port 4222 --auth-token TOKEN
magichop daemon --config ~/.config/magichop/config.json
magichop claim [device-or-address] [--json] [--timeout 11s]
magichop release [device-or-address] [--json]
magichop status [device-or-address] [--json]
magichop devices [--scan] [--json]
magichop doctor [--json]
magichop install mac
magichop install raycast [--dir DIR]
magichop uninstall mac
magichop edit
magichop upgrade [--check] [--version VERSION] [--yes]
magichop version
```

## Files

| Path | Purpose |
| --- | --- |
| `~/.config/magichop/config.json` | Config and shared token |
| `~/Library/Application Support/MagicHop/state.jsonl` | Accepted and final claim records |
| `~/Library/Logs/MagicHop/daemon.jsonl` | Daemon logs |
| `$TMPDIR/magichop-$UID/daemon.sock` | Local CLI socket |
| `~/Library/LaunchAgents/dev.magichop.daemon.plist` | macOS LaunchAgent |

## Upgrade

Check for the latest release:

```bash
magichop upgrade --check
```

Upgrade from GitHub Releases:

```bash
magichop upgrade --yes
```

MagicHop downloads the platform archive and `checksums.sha256.txt`, verifies SHA-256, replaces the current binary, and restarts the LaunchAgent when installed.

## Versioning

This project uses [Semantic Versioning](https://semver.org).

## License

[MIT](https://github.com/yegor-usoltsev/magichop/blob/main/LICENSE)
