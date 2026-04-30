//go:build coverage

package main

// This file is only included when building with -tags=coverage.
// It starts a CoverPort HTTP server on port 9095 that allows collecting
// code coverage data from the running binary during E2E tests.
// Production builds without -tags=coverage are completely unaffected.

import _ "github.com/konflux-ci/coverport/instrumentation/go" // starts coverage server via init()
