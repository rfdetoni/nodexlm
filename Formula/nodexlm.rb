class Nodexlm < Formula
  desc "Local distributed GGUF inference"
  homepage "https://github.com/rfdetoni/nodexlm"
  version "0.1.0-edge.35"
  license "AGPL-3.0-only"
  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/rfdetoni/nodexlm/releases/download/nodexlm-preview-274ef979/nodexlm_darwin_arm64.tar.gz"
      sha256 "cd22a9ce77eb4db7fe77ba241d99b742fd0ff0630db1bbe2f666a1f3b2f0d6eb"
    end
    if Hardware::CPU.intel?
      url "https://github.com/rfdetoni/nodexlm/releases/download/nodexlm-preview-274ef979/nodexlm_darwin_amd64.tar.gz"
      sha256 "a39244ac93c7f8813ea80ce349d45642f0d5781085134f444f6e81cc921e67bd"
    end
  end
  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/rfdetoni/nodexlm/releases/download/nodexlm-preview-274ef979/nodexlm_linux_arm64.tar.gz"
      sha256 "94cf09e5af67d650e62270fde31e1dab53f2f2e1d638d0aad7cad8a7c70f1535"
    end
    if Hardware::CPU.intel?
      url "https://github.com/rfdetoni/nodexlm/releases/download/nodexlm-preview-274ef979/nodexlm_linux_amd64.tar.gz"
      sha256 "001a899beffd9f4ad4708708daca88e7459fa997c79bf3220097cbf49758937f"
    end
  end
  def install
    bin.install "nodexlm"
  end
  test do
    assert_match version.to_s, shell_output("#{bin}/nodexlm version")
  end
end
