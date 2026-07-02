# dumbvpn

A single-purpose userspace VPN client (Go + [GOST](https://github.com/go-gost/gost)) that
tunnels all system traffic through one fixed relay server. There's no config file —
the server address, credentials, and bypass rules are all baked into the binary at
build time.

## Quick install (Windows)

This is a private repo, so you need a
[GitHub Personal Access Token](https://github.com/settings/tokens) with `repo`
read access first. Then, in an ordinary (non-admin) PowerShell:

```powershell
$env:GITHUB_TOKEN = "ghp_xxx"
irm -Headers @{Authorization="token $($env:GITHUB_TOKEN)"} `
  https://raw.githubusercontent.com/wprhvso/dumbvpn/main/install.ps1 | iex
```

The script will:

- ask for Administrator elevation (needed to install the Windows service)
- download `dumbvpn.exe` and `wintun.dll` from the latest GitHub Release
- install everything to `C:\Program Files\DumbVPN\` and register/start it as
  an auto-starting Windows service named `DumbVPN`

Releases are built and published from `.github/workflows/release.yml`, run
manually via `Actions → Release → Run workflow` on a version tag (e.g.
`v0.1.3`).

Check status any time with `Get-Service DumbVPN`.

To uninstall:

```powershell
irm https://raw.githubusercontent.com/wprhvso/dumbvpn/main/uninstall.ps1 | iex
```

## Linux

Not built yet — a one-command installer for Linux is planned but not
implemented in this pass.

## NixOS

Use `flake.nix`'s `nixosModules.default`, which wires `services.dumbvpn.enable`
up to a systemd unit directly — don't use the Windows installer scripts above.
