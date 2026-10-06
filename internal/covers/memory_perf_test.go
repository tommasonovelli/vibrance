//go:build perf

package covers

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPerfThumbnailMemory measures the memory the making of thumbnails takes
// in the two worst cases the decode slots allow (T20): one cover of 40
// megapixels, the largest a thumbnail is made of, which is decoded alone,
// and two square covers of 16 megapixels (alonePixels), the largest that
// are decoded together. A figure is what the process holds more than before, at
// its highest; the worst case of the server is the largest of them. It is
// part of the performance suite (scripts/perf.sh), which reports the
// figures; nothing is a budget here.
func TestPerfThumbnailMemory(t *testing.T) {
	for _, c := range []struct {
		name          string
		depth, colors byte // of the PNG header: bits for each sample, and the color type
		samples       int  // for each pixel
	}{
		{"8 bits, RGB", 8, 2, 3},
		{"8 bits, RGBA", 8, 6, 4},
		{"16 bits, RGBA", 16, 6, 4},
	} {
		for _, m := range []struct {
			what          string
			width, height int
			together      int
		}{
			{"one thumbnail of a 40-megapixel PNG", 8000, 5000, 1},
			// A square is the worst shape: the scaling keeps a buffer of
			// the width of the thumbnail by the height of the cover.
			{"one thumbnail of a square 40-megapixel PNG", 6324, 6324, 1},
			{"two thumbnails of 16-megapixel PNGs at once", 4000, 4000, decodeSlots},
		} {
			data := flatPNG(t, m.width, m.height, c.depth, c.colors, c.samples)
			if got := slotsFor(bytes.NewReader(data)) * int64(m.together); got != decodeSlots {
				t.Fatalf("%s: %d of them take %d decode slots, want all %d", m.what, m.together, got, decodeSlots)
			}
			runtime.GC()
			debug.FreeOSMemory()
			before := residentKB(t)
			peak := before
			var sampler sync.WaitGroup
			stop := make(chan struct{})
			sampler.Go(func() {
				for {
					select {
					case <-stop:
						return
					case <-time.After(2 * time.Millisecond):
						peak = max(peak, residentKB(t))
					}
				}
			})
			began := time.Now()
			var renders sync.WaitGroup
			for range m.together {
				renders.Go(func() {
					if thumb, err := render(data, int(Thumb640)); err != nil || len(thumb) == 0 {
						t.Errorf("%s, %s: %v", m.what, c.name, err)
					}
				})
			}
			renders.Wait()
			took := time.Since(began)
			close(stop)
			sampler.Wait()
			if t.Failed() {
				t.FailNow()
			}
			fmt.Printf("PERF | %-58s | %5s | %10s | %10.1f %-2s | %10s | %9s | %s\n",
				m.what+", "+c.name+": memory", "1", "", float64(peak-before)/1024, "MB", "", "-", "-")
			fmt.Printf("PERF | %-58s | %5s | %10s | %10.1f %-2s | %10s | %9s | %s\n",
				m.what+", "+c.name+": time", "1", "", took.Seconds(), "s", "", "-", "-")
		}
	}
}

// flatPNG is a valid PNG image of one color, of the depth and the color
// type given: small on disk whatever its size, and as large in memory as
// any image of that kind once decoded.
func flatPNG(t *testing.T, width, height int, depth, colors byte, samples int) []byte {
	t.Helper()
	ihdr := binary.BigEndian.AppendUint32(nil, uint32(width))
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(height))
	ihdr = append(ihdr, depth, colors, 0, 0, 0)
	out := append([]byte("\x89PNG\r\n\x1a\n"), pngChunk("IHDR", ihdr)...)
	var pixels bytes.Buffer
	z := zlib.NewWriter(&pixels)
	row := bytes.Repeat([]byte{0x80}, 1+width*samples*int(depth)/8)
	row[0] = 0 // the filter byte
	for range height {
		if _, err := z.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	out = append(out, pngChunk("IDAT", pixels.Bytes())...)
	return append(out, pngChunk("IEND", nil)...)
}

// residentKB is the memory the test process holds, from /proc.
func residentKB(t *testing.T) int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Error(err)
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				t.Error(err)
			}
			return kb
		}
	}
	return 0
}
