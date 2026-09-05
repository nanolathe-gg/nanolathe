// Package retailcat caches the compiled retail catalog for asset-gated tests.
//
// It is deliberately SEPARATE from internal/testsupport: this package imports
// internal/content, and content, cob and model import one another, so a
// content dependency inside testsupport itself would make those three
// packages' own tests an import cycle.
package retailcat
