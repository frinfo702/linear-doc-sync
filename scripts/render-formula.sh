#!/usr/bin/env sh
set -eu

if [ "$#" -ne 6 ]; then
  echo "usage: $0 <repo> <version> <darwin_arm64_sha> <darwin_amd64_sha> <linux_arm64_sha> <linux_amd64_sha>" >&2
  exit 1
fi

REPO="$1"
VERSION="$2"
TAG="v${VERSION}"
DARWIN_ARM64_SHA="$3"
DARWIN_AMD64_SHA="$4"
LINUX_ARM64_SHA="$5"
LINUX_AMD64_SHA="$6"

cat <<EOF
class Lsy < Formula
  desc "CLI to publish Linear documents to GitHub as Markdown"
  homepage "https://github.com/${REPO}"
  version "${VERSION}"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/${REPO}/releases/download/${TAG}/lsy_${VERSION}_darwin_arm64.tar.gz"
      sha256 "${DARWIN_ARM64_SHA}"
    else
      url "https://github.com/${REPO}/releases/download/${TAG}/lsy_${VERSION}_darwin_amd64.tar.gz"
      sha256 "${DARWIN_AMD64_SHA}"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/${REPO}/releases/download/${TAG}/lsy_${VERSION}_linux_arm64.tar.gz"
      sha256 "${LINUX_ARM64_SHA}"
    else
      url "https://github.com/${REPO}/releases/download/${TAG}/lsy_${VERSION}_linux_amd64.tar.gz"
      sha256 "${LINUX_AMD64_SHA}"
    end
  end

  def install
    bin.install "lsy"
  end

  def caveats
    <<~EOS
      Configuration and local Markdown clones are stored in:
        ~/.config/lsy

      ~/.config/lsy is created automatically on first run.
      Run \`lsy config init\` to set up your Linear API key and GitHub repository.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/lsy --version")
  end
end
EOF
