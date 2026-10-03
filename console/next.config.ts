import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Don't regenerate AGENTS.md/CLAUDE.md on every `next dev` — this is an
  // internal admin tool, not a repo meant to be edited by an AI coding
  // agent's own convention file.
  agentRules: false,
};

export default nextConfig;
