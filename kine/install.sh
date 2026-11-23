#!/bin/sh
# KineRouter installer for Linux/macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.sh | sh
#
# Downloads the release binary for this machine, verifies it against the
# release's checksums.txt, and installs it. The script itself is not verified
# when piped from a URL — pin a commit ref or audit it first if you need that.
#
# Env:
#   KINEROUTER_VERSION=X.Y.Z   install a specific release (default: newest kine-v*)
#   KINEROUTER_INSTALL_DIR=DIR install here (default: /usr/local/bin if writable,
#                              else ~/.local/bin)
#   KINEROUTER_REPO=owner/name override the release repository
set -eu

REPO=${KINEROUTER_REPO:-keyzakyy-dev/kinerouter}

die() { printf 'kinerouter: %s\n' "$1" >&2; exit 1; }
say() { printf 'kinerouter: %s\n' "$1" >&2; }

need_cmd() { command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"; }

fetch() { # fetch <url> <dest>
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		die "need curl or wget"
	fi
}

fetch_out() { # fetch_out <url>
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$1"
	else
		die "need curl or wget"
	fi
}

sha256() { # sha256 <file> -> hex on stdout
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/.*= //'
	else
		die "need sha256sum, shasum or openssl to verify the download"
	fi
}

# ---------------------------------------------------------------- platform

os=$(uname -s 2>/dev/null || echo unknown)
case "$os" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported OS: $os (release assets cover linux and darwin; Windows users: download the .exe from https://github.com/$REPO/releases)" ;;
esac

arch=$(uname -m 2>/dev/null || echo unknown)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported CPU architecture: $arch" ;;
esac

# ---------------------------------------------------------------- version

version=${KINEROUTER_VERSION:-}
version=${version#kine-v}
version=${version#v}
if [ -z "$version" ]; then
	need_cmd sed
	# /releases/latest would return the repo's newest release of any prefix, and
	# this repository also carries unprefixed vX.Y.Z tags, so pick the first
	# kine-v* entry.
	version=$(fetch_out "https://api.github.com/repos/$REPO/releases?per_page=50" |
		grep -o '"tag_name"[[:space:]]*:[[:space:]]*"kine-v[0-9][^"]*"' |
		head -n 1 |
		sed 's/.*"kine-v//; s/"$//')
	[ -n "$version" ] || die "could not find a kine-v* release in $REPO; pass KINEROUTER_VERSION=X.Y.Z"
fi

tag="kine-v$version"
asset="kinerouter_${version}_${os}_${arch}"
base=${KINEROUTER_BASE_URL:-https://github.com/$REPO/releases/download/$tag}

# ---------------------------------------------------------------- download

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t kinerouter)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "downloading $asset ($tag)"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "no checksums.txt for $tag"
fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"

want=$(grep "[[:space:]]\(\./\)\{0,1\}${asset}\$" "$tmp/checksums.txt" | head -n 1 | cut -d' ' -f1 || true)
[ -n "$want" ] || die "checksums.txt for $tag has no entry for $asset"
got=$(sha256 "$tmp/$asset")
[ "$want" = "$got" ] || die "sha256 mismatch for $asset (got $got, want $want)"

# ---------------------------------------------------------------- install

dir=${KINEROUTER_INSTALL_DIR:-}
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ] || { [ "$(id -u)" = 0 ] && [ -d /usr/local/bin ]; }; then
		dir=/usr/local/bin
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir" || die "cannot create $dir"
[ -w "$dir" ] || die "$dir is not writable; set KINEROUTER_INSTALL_DIR or re-run with appropriate permissions"

# One owner per command name. A second kinerouter earlier on PATH (usually the
# npm shim) would shadow this install and update on its own schedule, which is
# the version drift this warning exists to stop.
existing=$(command -v kinerouter 2>/dev/null || true)
case "$existing" in
	"" | "$dir/kinerouter") ;;
	*)
		say "warning: $existing also provides kinerouter and may win by PATH order"
		say "         remove it (npm rm -g kinerouter) so updates cannot drift apart"
		;;
esac

mv "$tmp/$asset" "$dir/kinerouter"
chmod 0755 "$dir/kinerouter"

say "installed $dir/kinerouter ($tag, sha256 verified)"

# Companion binary, delivered beside the gateway so `kinerouter tray` finds it
# as a sibling. Missing asset is not an install failure.
tray="kinerouter-tray_${version}_${os}_${arch}"
traywant=$(grep "[[:space:]]\(\./\)\{0,1\}${tray}\$" "$tmp/checksums.txt" | head -n 1 | cut -d' ' -f1 || true)
if [ -n "$traywant" ] && fetch "$base/$tray" "$tmp/$tray" 2>/dev/null; then
	traygot=$(sha256 "$tmp/$tray")
	if [ "$traywant" = "$traygot" ]; then
		mv "$tmp/$tray" "$dir/kinerouter-tray"
		chmod 0755 "$dir/kinerouter-tray"
		say "installed $dir/kinerouter-tray (sha256 verified)"
	else
		say "warning: tray sha256 mismatch, tray not installed"
	fi
fi
case ":$PATH:" in
	*":$dir:"*) ;;
	*) say "note: $dir is not on PATH; add it, e.g. export PATH=\"$dir:\$PATH\"" ;;
esac
say "start it with: INITIAL_PASSWORD='choose-something' kinerouter"
