import { loader } from "fumadocs-core/source";
import { defineDocs } from "fumadocs-mdx/macro";
import { docsPath } from "./project";

const docs = defineDocs({ dir: "content/docs" });

export const source = loader({
  baseUrl: docsPath,
  source: docs.toFumadocsSource(),
});
