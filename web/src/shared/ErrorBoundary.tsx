import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  /** What to show in place of the zone that failed to render, with a way to try it again. */
  fallback: (retry: () => void) => ReactNode;
  /** What the zone draws, by identity: when it changes while the zone is in error, the zone is tried again. */
  resetKey?: unknown;
  children: ReactNode;
}

/** Keeps a rendering error inside its zone: the rest of the page stays usable. */
export class ErrorBoundary extends Component<Props, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidUpdate(prev: Props, prevState: { failed: boolean }) {
    // Only a zone that was already in error is tried again: one that fails on this very update has just been given this data.
    if (prevState.failed && this.state.failed && prev.resetKey !== this.props.resetKey) this.setState({ failed: false });
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("agent-stats: rendering failed", error, info.componentStack);
  }

  render() {
    return this.state.failed ? this.props.fallback(() => this.setState({ failed: false })) : this.props.children;
  }
}
