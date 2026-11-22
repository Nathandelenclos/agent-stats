import { createMDX } from "fumadocs-mdx/next";

export default createMDX()({
  output: "export",
  trailingSlash: true,
  experimental: { cpus: 1 },
  images: { unoptimized: true },
});
