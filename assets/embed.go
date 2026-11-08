// Package assets holds the compose stack and image build contexts, compiled
// into the sandbox binary so it runs without a checkout of this repo.
package assets

import "embed"

// FS holds compose.yaml and the gateway and image build contexts.
//
//go:embed compose.yaml gateway image
var FS embed.FS
