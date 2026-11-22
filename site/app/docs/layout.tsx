import type { ReactNode } from "react";
import { DocsLayout } from "fumadocs-ui/layouts/docs";
import { navigation } from "../../lib/navigation";
import { source } from "../../lib/source";

export default function DocumentationLayout({ children }: { children: ReactNode }) {
  return <DocsLayout tree={source.getPageTree()} {...navigation}>{children}</DocsLayout>;
}
