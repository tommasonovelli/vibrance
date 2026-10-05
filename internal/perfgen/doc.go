// Package perfgen is the performance suite (DESIGN.md §14, step S24): a
// generator of a synthetic index of the size the budgets are stated for, and
// the tests that measure the server on it and fail when a budget is missed.
//
// Its other files carry the build tag `perf`: they run only in
// scripts/perf.sh, which takes minutes and needs a quiet machine. The gate
// vets them with `-tags perf` and never runs them.
package perfgen
