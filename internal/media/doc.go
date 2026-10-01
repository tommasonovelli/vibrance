// Package media is the adapter of the external tools, ffprobe and ffmpeg
// (DESIGN.md §3.2): the Runner that starts every process, the probe of a
// track file, the mapping of its tags and its audio fingerprint (§5.4).
//
// Every tool runs through the Runner, with a context, a timeout, a bounded
// standard error and the file to read on a descriptor, never as a path
// (I8, T2). The tools are the pinned ones: NewTools refuses any other
// version.
//
// The package knows the tools and the formats. It knows nothing of the
// domain: no album, no index, no library.
package media
