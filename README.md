# MagicHop

[![Build Status](https://github.com/yegor-usoltsev/magichop/actions/workflows/ci.yml/badge.svg)](https://github.com/yegor-usoltsev/magichop/actions)
[![Codecov](https://codecov.io/github/yegor-usoltsev/magichop/graph/badge.svg?token=5I7K9PUI0P)](https://codecov.io/github/yegor-usoltsev/magichop)
[![GitHub Release](https://img.shields.io/github/v/release/yegor-usoltsev/magichop?sort=semver)](https://github.com/yegor-usoltsev/magichop/releases)
[![Docker Image (docker.io)](https://img.shields.io/docker/v/yusoltsev/magichop?label=docker.io&sort=semver)](https://hub.docker.com/r/yusoltsev/magichop)
[![Docker Image (ghcr.io)](https://img.shields.io/docker/v/yusoltsev/magichop?label=ghcr.io&sort=semver)](https://github.com/yegor-usoltsev/magichop/pkgs/container/magichop)
[![Docker Image Size](https://img.shields.io/docker/image-size/yusoltsev/magichop?sort=semver&arch=amd64)](https://hub.docker.com/r/yusoltsev/magichop/tags)

MagicHop lets several Macs share Apple Magic peripherals, such as Magic Keyboard, Magic Mouse, and Magic Trackpad, without talking to each other directly. A coordinator runs NATS, each Mac runs a small daemon, and `magichop claim` asks other Macs to release a peripheral before acquiring it locally.

Releasing means unpairing the peripheral from the current Mac. Acquiring means clearing stale local pairing state, pairing, connecting, and verifying the connected state.

## Usage

### Coordinator

Run the coordinator on an always-on host:

```bash
docker run -d \
  --name magichop \
  --restart unless-stopped \
  -e MAGICHOP_AUTH_TOKEN=<shared-token> \
  -p 4222:4222 \
  ghcr.io/yegor-usoltsev/magichop:latest
```

Tail coordinator logs:

```bash
docker logs -f magichop
```

### Macs

Put the release binary somewhere that will not move:

```bash
mkdir -p ~/.local/bin
cp magichop ~/.local/bin/magichop
```

Find Bluetooth addresses for paired Magic peripherals:

```bash
brew install blueutil
blueutil --paired
```

Create a config file on each Mac:

```bash
magichop config init \
  --force \
  --coordinator-url nats://coordinator.local:4222 \
  --auth-token <shared-token> \
  --device keyboard=<magic-keyboard-address> \
  --device trackpad=<magic-trackpad-address> \
  --default-device trackpad
```

Install the macOS LaunchAgent:

```bash
magichop install mac
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

## Raycast

Generate a Raycast Script Command:

```bash
magichop install raycast --dir ~/raycast-scripts
```

Add that directory in Raycast under Settings -> Extensions -> Script Commands -> Add Script Directory.

## Environment variables

Coordinator:

| KEY                    | TYPE     | DEFAULT   | REQUIRED |
| ---------------------- | -------- | --------- | -------- |
| `MAGICHOP_SERVER_HOST` | `string` | `0.0.0.0` | Yes      |
| `MAGICHOP_SERVER_PORT` | `uint16` | `4222`    | Yes      |
| `MAGICHOP_AUTH_TOKEN`  | `string` |           | Yes      |

Mac clients use `MAGICHOP_CONFIG` when it is set. Otherwise they read `~/.config/magichop/config.json`.

## Configuration

```json
{
  "node_name": "",
  "coordinator_url": "nats://coordinator.local:4222",
  "auth_token": "CHANGE_ME",
  "devices": {
    "keyboard": "aa-bb-cc-dd-ee-01",
    "trackpad": "aa-bb-cc-dd-ee-02"
  },
  "default_device": "trackpad",
  "claim_timeout": "6s",
  "connect_timeout": "15s",
  "disconnect_timeout": "6s"
}
```

If `node_name` is empty, MagicHop uses the hostname. Device aliases are local names for Magic Keyboard, Magic Mouse, Magic Trackpad, or other Bluetooth MAC addresses.

`claim_timeout` bounds each peer-release round. `connect_timeout` bounds the full local acquisition budget. MagicHop keeps the normal path short, but a temporarily stuck peripheral can still use the configured budget before the command fails.

## Commands

```bash
magichop coordinator
magichop daemon
magichop claim [device]
magichop status
magichop config init
magichop install mac
magichop install raycast --dir <dir>
magichop uninstall mac
magichop upgrade
magichop version
```

## Versioning

This project uses [Semantic Versioning](https://semver.org).

## License

[MIT](https://github.com/yegor-usoltsev/MagicHop/blob/main/LICENSE)
