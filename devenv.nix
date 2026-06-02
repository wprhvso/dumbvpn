{ pkgs, ... }:

let
  gomod2nix-src = builtins.fetchTarball "https://github.com/amarbel-llc/gomod2nix/archive/7a765734636281d59e12f9ecabaa8c8a5746709a.tar.gz";
  gomod2nix-overlay = import "${gomod2nix-src}/overlay.nix";
  pkgs-with-gost = import pkgs.path {
    inherit (pkgs.stdenv.hostPlatform) system;
    overlays = [ gomod2nix-overlay ];
    config.allowUnfree = true;
  };
  gomod2nix-pkg = pkgs-with-gost.gomod2nix;
in
{
  packages = with pkgs; [
    gradle
    scrcpy
    android-tools
    git
    just
    gomod2nix-pkg
  ];

  languages.go.enable = true;
  languages.java = {
    enable = true;
    jdk.package = pkgs.jdk17;
  };

  android = {
    enable = true;
    platforms.version = [ "34" ];
    buildTools.version = [ "34.0.0" ];
    ndk.enable = true;

    emulator.enable = true;
    systemImages.enable = true;
    systemImageTypes = [ "google_apis_playstore" ];
    abis = [ "x86_64" ];
  };
}
