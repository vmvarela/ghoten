# typed: false
# frozen_string_literal: true

# This formula is auto-updated by the release workflow.
# Do not edit version or sha256 values manually.

class Ghoten < Formula
  desc 'OpenTofu fork with native ORAS backend for OCI registry state storage'
  homepage 'https://github.com/vmvarela/ghoten'
  version '1.13.1'
  license 'MPL-2.0'

  if OS.mac? && Hardware::CPU.intel?
    url "https://github.com/vmvarela/ghoten/releases/download/v#{version}/ghoten_#{version}_darwin_amd64.tar.gz"
    sha256 'PLACEHOLDER'
  elsif OS.mac? && Hardware::CPU.arm?
    url "https://github.com/vmvarela/ghoten/releases/download/v#{version}/ghoten_#{version}_darwin_arm64.tar.gz"
    sha256 'PLACEHOLDER'
  elsif OS.linux? && Hardware::CPU.intel?
    url "https://github.com/vmvarela/ghoten/releases/download/v#{version}/ghoten_#{version}_linux_amd64.tar.gz"
    sha256 'PLACEHOLDER'
  elsif OS.linux? && Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
    url "https://github.com/vmvarela/ghoten/releases/download/v#{version}/ghoten_#{version}_linux_arm64.tar.gz"
    sha256 'PLACEHOLDER'
  else
    odie 'Unsupported platform! ghoten supports macOS (Intel/Apple Silicon) and Linux (amd64/arm64).'
  end

  def install
    bin.install 'ghoten'
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ghoten version")
  end
end
