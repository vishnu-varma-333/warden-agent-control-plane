import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Don't regenerate AGENTS.md/CLAUDE.md on every `next dev` — this is an
  // internal admin tool, not a repo meant to be edited by an AI coding
  // agent's own convention file.
  agentRules: false,
  // A self-contained server bundle (node_modules trimmed to only what's
  // actually used) for the production Docker image — see Dockerfile.
  output: "standalone",
};

export default nextConfig;
