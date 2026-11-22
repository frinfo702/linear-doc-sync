class Lsy < Formula
  desc "CLI to publish Linear documents to GitHub as Markdown"
  homepage "https://github.com/frinfo702/linear-doc-sync"
  version "0.1.1"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.1.1/lsy_0.1.1_darwin_arm64.tar.gz"
      sha256 "563ea72016ada1c7e3ea255170dcd91933efa9bdca7e6efe566da61531a00bdb"
    else
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.1.1/lsy_0.1.1_darwin_amd64.tar.gz"
      sha256 "3904bc9353fdfb8baab5e522a2f954874172fe3212ab83b4cadc3c6948e1b239"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.1.1/lsy_0.1.1_linux_arm64.tar.gz"
      sha256 "a9f760a14c750b91cc86d09f2102f794102a901668e199fa9cdbc73bd80af6c4"
    else
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.1.1/lsy_0.1.1_linux_amd64.tar.gz"
      sha256 "84fd94a21a5985331b2a56c0aaa1dc4731a984c7ea7ce745302a920907d425c0"
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
    assert_match version.to_s, shell_output("#{bin}/lsy --version")
  end
end
