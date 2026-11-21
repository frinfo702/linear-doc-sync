class Lsy < Formula
  desc "CLI to publish Linear documents to GitHub as Markdown"
  homepage "https://github.com/frinfo702/linear-doc-sync"
  version "0.1.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.1.0/lsy_0.1.0_darwin_arm64.tar.gz"
      sha256 "dea235e71e730db6f5407add2efd8788c48e454fd8baf9cc5adfa1683979726e"
    else
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.1.0/lsy_0.1.0_darwin_amd64.tar.gz"
      sha256 "fcabbfc3ea8a60e729e6d712f87cf37ee99ad78347610429366339bb5600780e"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.1.0/lsy_0.1.0_linux_arm64.tar.gz"
      sha256 "205f647760f9bb56ad272b4f98c60182dba564e973c271979f4c86e2e453c68f"
    else
      url "https://github.com/frinfo702/linear-doc-sync/releases/download/v0.1.0/lsy_0.1.0_linux_amd64.tar.gz"
      sha256 "a1606675ba90fbecea99fb3c473ec94cced5b0754470b6d864d2ab753dfea057"
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
