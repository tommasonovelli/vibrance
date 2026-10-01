// Package library reads the MusicLib library: confined access through
// os.Root, the receipt, file classification, the pure reconciliation, the
// indexer and the scanner (DESIGN.md §4, §6). It knows nothing of HTTP or api.
//
// Every access to the disk goes through a [Root]. Nothing else in this
// package, and nothing in the rest of the server, opens, lists or stats a
// path of the library. The parsers and the classification are pure.
package library
