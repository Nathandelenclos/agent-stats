// React only accepts updates made inside act() when the environment says it is a test one.
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

import { beforeEach } from "vitest";

// The page keeps its filters in the address: start every test from the bare one.
beforeEach(() => window.history.replaceState(null, "", "/"));
