class Lsy < Formula
  desc "CLI to publish Linear documents to GitHub as Markdown"
  homepage "https://github.com/frinfo702/linear-doc-sync"
  version "0.0.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.0.0/lsy_0.0.0_darwin_arm64.tar.gz"
      sha256 "REPLACE_WITH_DARWIN_ARM64_SHA256"
    else
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.0.0/lsy_0.0.0_darwin_amd64.tar.gz"
      sha256 "REPLACE_WITH_DARWIN_AMD64_SHA256"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.0.0/lsy_0.0.0_linux_arm64.tar.gz"
      sha256 "REPLACE_WITH_LINUX_ARM64_SHA256"
    else
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.0.0/lsy_0.0.0_linux_amd64.tar.gz"
      sha256 "REPLACE_WITH_LINUX_AMD64_SHA256"
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
      Run `lsy config init` to set up your Linear API key and GitHub repository.
    EOS
  end

  test do
    assert_match "lsy", shell_output("#{bin}/lsy --version")
  end
end
