// Package covers serves the covers of the albums: the file MusicLib wrote,
// or a thumbnail of it, made on request and kept in a cache on disk
// (DESIGN.md §9.2).
//
// It reads the library only through a library.Root and writes only in its
// cache folder. It knows nothing of HTTP.
package covers
