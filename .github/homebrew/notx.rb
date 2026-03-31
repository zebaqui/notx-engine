# frozen_string_literal: true

# This file is the canonical template for the notx Homebrew formula.
# The live copy lives in the tap repository: zebaqui/homebrew-notx
# (Formula/notx.rb) and is auto-updated by the release workflow on every
# "v*" tag.  This copy is kept here purely as a reference / seed.
#
# To install manually before the tap exists:
#   brew install --formula .github/homebrew/notx.rb
class Notx < Formula
  desc "notx — lightweight event-streaming / notification engine"
  homepage "https://github.com/zebaqui/notx-engine"
  version "0.0.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/zebaqui/notx-engine/releases/download/v#{version}/notx-v#{version}-darwin-arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    else
      url "https://github.com/zebaqui/notx-engine/releases/download/v#{version}/notx-v#{version}-darwin-amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    arch = Hardware::CPU.arm? ? "arm64" : "amd64"
    bin.install "notx-darwin-#{arch}" => "notx"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/notx version")
  end
end
