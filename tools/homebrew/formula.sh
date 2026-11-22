#!/usr/bin/env bash
set -euo pipefail

tag=$1
manifest=$2
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Expected a stable release tag, got $tag" >&2
  exit 1
fi

version=${tag#v}
base="https://github.com/Nathandelenclos/agent-stats/releases/download/$tag"
intel_archive="agent-stats_${version}_darwin_amd64.tar.gz"
arm_archive="agent-stats_${version}_darwin_arm64.tar.gz"

checksum() {
  local archive=$1
  awk -v archive="$archive" '
    $2 == archive { count++; digest = $1 }
    END {
      if (count != 1 || length(digest) != 64 || digest !~ /^[0-9a-f]+$/) exit 1
      print digest
    }
  ' "$manifest" || { echo "Expected one SHA-256 for $archive" >&2; return 1; }
}

intel_sha=$(checksum "$intel_archive")
arm_sha=$(checksum "$arm_archive")

cat <<RUBY
class AgentStats < Formula
  desc "Usage statistics for AI coding agents from local session logs"
  homepage "https://github.com/Nathandelenclos/agent-stats"
  version "$version"
  license "MIT"

  on_intel do
    url "$base/$intel_archive"
    sha256 "$intel_sha"
  end

  on_arm do
    url "$base/$arm_archive"
    sha256 "$arm_sha"
  end

  depends_on :macos

  def install
    bin.install "agent-stats"
  end

  test do
    assert_match "agent-stats", shell_output("#{bin}/agent-stats --help")
  end
end
RUBY
