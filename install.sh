#!/usr/bin/env sh
set -eu

REPO="${LSY_REPO:-frinfo702/linear-doc-sync}"
BINARY="lsy"
VERSION="${VERSION:-latest}"
CONFIG_DIR="${LSY_CONFIG_DIR:-$HOME/.config/lsy}"

info() {
  printf '%s\n' "$*"
}

err() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "required command not found: $1"
  fi
}

detect_os() {
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in
    darwin) echo "darwin" ;;
    linux) echo "linux" ;;
    *) err "unsupported operating system: $os" ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) err "unsupported architecture: $arch" ;;
  esac
}

resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION" | sed 's/^v//'
    return
  fi

  tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -n 1)"

  if [ -z "$tag" ]; then
    err "could not determine latest release version from https://github.com/${REPO}/releases"
  fi
  echo "$tag" | sed 's/^v//'
}

verify_checksum() {
  archive_path="$1"
  checksums_path="$2"
  archive_name="$(basename "$archive_path")"

  expected="$(awk -v name="$archive_name" '$2 == name { print $1 }' "$checksums_path" | head -n 1)"
  if [ -z "$expected" ]; then
    err "checksum for ${archive_name} not found in checksums.txt"
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive_path" | awk '{ print $1 }')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$archive_path" | awk '{ print $1 }')"
  else
    info "warning: neither sha256sum nor shasum found; skipping checksum verification"
    return 0
  fi

  if [ "$actual" != "$expected" ]; then
    err "checksum mismatch for ${archive_name} (expected ${expected}, got ${actual})"
  fi
}

choose_install_dir() {
  if [ -n "${INSTALL_DIR:-}" ]; then
    echo "$INSTALL_DIR"
    return
  fi
  if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
    echo "/usr/local/bin"
    return
  fi
  echo "$HOME/.local/bin"
}

main() {
  need_cmd curl
  need_cmd tar
  need_cmd uname

  os="$(detect_os)"
  arch="$(detect_arch)"
  ver="$(resolve_version)"
  tag="v${ver}"

  archive="${BINARY}_${ver}_${os}_${arch}.tar.gz"
  base_url="https://github.com/${REPO}/releases/download/${tag}"

  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT INT TERM

  info "Downloading ${BINARY} ${tag} (${os}/${arch})..."
  curl -fsSL "${base_url}/${archive}" -o "${tmp_dir}/${archive}"
  curl -fsSL "${base_url}/checksums.txt" -o "${tmp_dir}/checksums.txt"

  verify_checksum "${tmp_dir}/${archive}" "${tmp_dir}/checksums.txt"

  tar -xzf "${tmp_dir}/${archive}" -C "$tmp_dir"
  if [ ! -f "${tmp_dir}/${BINARY}" ]; then
    err "binary ${BINARY} not found in archive ${archive}"
  fi

  install_dir="$(choose_install_dir)"
  mkdir -p "$install_dir"
  chmod +x "${tmp_dir}/${BINARY}"
  mv "${tmp_dir}/${BINARY}" "${install_dir}/${BINARY}"

  # Initialize ~/.config/lsy directory and default config.json.
  mkdir -p "${CONFIG_DIR}/docs"
  "${install_dir}/${BINARY}" config path >/dev/null 2>&1 || true

  info ""
  info "Installed ${BINARY} ${tag} to ${install_dir}/${BINARY}"
  info "Config directory initialized at ${CONFIG_DIR}"

  case ":${PATH}:" in
    *":${install_dir}:"*) ;;
    *)
      info ""
      info "Add ${install_dir} to your PATH:"
      info "  export PATH=\"${install_dir}:\$PATH\""
      ;;
  esac

  info ""
  info "Run '${BINARY} config init' to configure Linear and GitHub credentials."
}

main "$@"
