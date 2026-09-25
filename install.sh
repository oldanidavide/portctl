#!/bin/sh
# Installs portctl on Linux from the GitHub Releases page, with the system's
# package manager when it has one (apt, dnf, yum, zypper, pacman), so that
# tab completion works right away and removing it is the usual command.
#
#   curl -fsSL https://raw.githubusercontent.com/oldanidavide/portctl/main/install.sh | sh
#
# Settings, as environment variables (put them after the pipe: ... | PORTCTL_VERSION=0.1.0 sh):
#   PORTCTL_VERSION   version to install, like 0.1.0 (default: the latest)
#   PORTCTL_METHOD    package, or binary to only copy the program (default: package)
#   PORTCTL_BIN_DIR   where "binary" puts the program (default: /usr/local/bin)
set -eu

repo=oldanidavide/portctl
base=${PORTCTL_DOWNLOAD_URL:-https://github.com/$repo/releases/download}
method=${PORTCTL_METHOD:-package}
bin_dir=${PORTCTL_BIN_DIR:-/usr/local/bin}

say() { printf '%s\n' "$*"; }
die() { printf '✗ %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

# Wrapped in a function so that nothing runs if the download is cut short.
main() {
  case $(uname -s) in
    Linux) ;;
    Darwin) die "On macOS, install with Homebrew: brew install oldanidavide/tap/portctl" ;;
    *) die "unsupported system: $(uname -s) (portctl runs on Linux and macOS)" ;;
  esac

  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported processor: $(uname -m) (portctl is built for x86_64 and arm64)" ;;
  esac

  has curl || die "curl is required"

  if [ "$(id -u)" = 0 ]; then
    sudo=
  elif has sudo; then
    sudo=sudo
  else
    die "run this as root, or install sudo"
  fi

  version=${PORTCTL_VERSION:-}
  if [ -z "$version" ]; then
    # The latest release page redirects to .../tag/vX.Y.Z; no API call needed.
    url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
      die "cannot reach GitHub"
    version=${url##*/tag/}
    [ "$version" != "$url" ] || die "no release published yet"
  fi
  version=${version#v}

  file=
  if [ "$method" = package ]; then
    if has pacman; then
      file=portctl_${version}_linux_$arch.pkg.tar.zst
      install_cmd="pacman -U --noconfirm"
    elif has apt-get; then
      file=portctl_${version}_linux_$arch.deb
      install_cmd="apt-get install -y"
    elif has dnf; then
      file=portctl_${version}_linux_$arch.rpm
      install_cmd="dnf install -y"
    elif has yum; then
      file=portctl_${version}_linux_$arch.rpm
      install_cmd="yum install -y"
    elif has zypper; then
      file=portctl_${version}_linux_$arch.rpm
      install_cmd="zypper --non-interactive install --allow-unsigned-rpm"
    fi
  elif [ "$method" != binary ]; then
    die "PORTCTL_METHOD must be package or binary, not: $method"
  fi
  [ -n "$file" ] || file=portctl_${version}_linux_$arch.tar.gz

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

  say "Downloading portctl $version ($file)"
  curl -fsSL -o "$tmp/$file" "$base/v$version/$file" || die "download failed: $base/v$version/$file"
  curl -fsSL -o "$tmp/checksums.txt" "$base/v$version/checksums.txt" || die "download failed: checksums.txt"

  sum=$(awk -v f="$file" '$2 == f { print $1 }' "$tmp/checksums.txt")
  [ -n "$sum" ] || die "$file is not listed in checksums.txt"
  if has sha256sum; then
    got=$(sha256sum "$tmp/$file" | awk '{ print $1 }')
  else
    got=$(openssl dgst -sha256 "$tmp/$file" | awk '{ print $NF }')
  fi
  [ "$got" = "$sum" ] || die "checksum mismatch for $file: the download is damaged, try again"

  case $file in
    *.tar.gz)
      tar -xzf "$tmp/$file" -C "$tmp" portctl
      say "Installing to $bin_dir/portctl"
      if [ -w "$bin_dir" ] || { [ ! -e "$bin_dir" ] && mkdir -p "$bin_dir" 2>/dev/null; }; then
        install -m 0755 "$tmp/portctl" "$bin_dir/portctl"
      else
        $sudo mkdir -p "$bin_dir"
        $sudo install -m 0755 "$tmp/portctl" "$bin_dir/portctl"
      fi
      ;;
    *)
      say "Installing with: $install_cmd"
      # apt and dnf need a path with a slash to install a local file; the
      # package manager may run as another user, so make the file readable.
      chmod 0755 "$tmp"
      chmod 0644 "$tmp/$file"
      # shellcheck disable=SC2086 # install_cmd holds a command and its options
      $sudo $install_cmd "$tmp/$file"
      ;;
  esac

  say ""
  say "✓ portctl $version installed"
  case $file in
    *.tar.gz)
      has portctl || say "  $bin_dir is not on your PATH: add it to run portctl from any folder."
      say "  Tab completion: see https://github.com/$repo#tab-completion"
      ;;
    *) say "  Tab completion works in new terminals." ;;
  esac
  say "  Next: portctl config init"
}

main "$@"
