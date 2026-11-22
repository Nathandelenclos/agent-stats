// Package web is the browser input adapter of the reporting context: a local HTTP server that exposes the usage report
// and the sessions list as a small read-only JSON API, serves the single-page front over it, and runs the indexing in
// the background while the page watches its progress.
// It holds transport and presentation concerns only (routing, validation, headers, JSON shapes); every figure comes
// from the reporting use cases.
//
// The index holds the text of the prompts, so the server is built for one user on one machine: it binds to a loopback
// address unless asked otherwise (see ValidateAddr), accepts only requests addressed to a loopback host name against DNS
// rebinding, answers only GET and HEAD, sets no CORS header and serves a strict Content-Security-Policy.
package web
