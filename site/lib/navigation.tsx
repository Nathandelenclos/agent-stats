import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";
import { Activity } from "lucide-react";
import { docsPath, releases, repository } from "./project";

export const navigation: BaseLayoutProps = {
  nav: {
    title: (
      <span className="brand">
        <span className="brand-mark"><Activity size={19} aria-hidden="true" /></span>
        agent-stats
      </span>
    ),
  },
  links: [
    { text: "Documentation", url: docsPath, active: "url" },
    { text: "Releases", url: releases },
  ],
  githubUrl: repository,
};
