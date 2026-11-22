import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  /** What to show in place of the zone that failed to render, with a way to try it again. */
  fallback: (retry: () => void) => ReactNode;
  children: ReactNode;
}

/** Keeps a rendering error inside its zone: the rest of the page stays usable. */
export class ErrorBoundary extends Component<Props, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("agent-stats: rendering failed", error, info.componentStack);
  }

  render() {
    return this.state.failed ? this.props.fallback(() => this.setState({ failed: false })) : this.props.children;
  }
}
