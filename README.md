# dumbvpn

A single-purpose userspace VPN client (Go + [GOST](https://github.com/go-gost/gost)) that
tunnels all system traffic through one fixed relay server. There's no config file —
the server address, credentials, and bypass rules are all baked into the binary at
build time.

## Quick install (Windows)

This is a private repo, so you need a
[GitHub Personal Access Token](https://github.com/settings/tokens) with `repo`
read access first — the token is only needed once, up front, to fetch the
script itself from the private raw content endpoint. Then, in an ordinary
(non-admin) PowerShell:

```powershell
irm -Headers @{Authorization="token ghp_xxx"} `
  https://raw.githubusercontent.com/wprhvso/dumbvpn/main/install.ps1 | iex
```

The script will:

- prompt for your GitHub Personal Access Token on first run (via `Read-Host`,
  not an environment variable) and cache it encrypted at
  `%LOCALAPPDATA%\DumbVPN\github-token.xml`, so later runs and reinstalls
  don't ask again
- ask for Administrator elevation (needed to install the Windows service)
- download `dumbvpn.exe` and `wintun.dll` from the latest GitHub Release
- install everything to `C:\Program Files\DumbVPN\` and register/start it as
  an auto-starting Windows service named `DumbVPN`

To install a specific release instead of `latest`, pass `-Version`. Since
`irm | iex` can't forward arguments directly, wrap it in a script block:

```powershell
& ([scriptblock]::Create((irm -Headers @{Authorization="token ghp_xxx"} `
  https://raw.githubusercontent.com/wprhvso/dumbvpn/main/install.ps1))) -Version v0.1.3
```

If you've saved the script locally instead, it's just:

```powershell
.\install.ps1 -Version v0.1.3
```

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
